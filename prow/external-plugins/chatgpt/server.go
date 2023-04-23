/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/sashabaranov/go-openai"
	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/pluginhelp"
	"k8s.io/test-infra/prow/plugins"
)

const (
	pluginName             = "chatgpt"
	gitHostBaseURL         = "https://github.com"
	openaiSystemMessage    = "You are an experienced software developer. You will act as a reviewer for a GitHub Pull Request, and you should answer by markdown format."
	openaiMessageMaxLen    = 9000
	openaiQuestionForeword = "Please help me to review the github pull request: summarize the key changes and identify potential problems, then give some fixing suggestions, all you output should be markdown."
	botHeadnote            = `> **I have already done a preliminary review for you, and I hope to help you do a better job.**
------
`
)

var chatgptRe = regexp.MustCompile(`(?m)^/chatgpt\s+(.+)$`)

type githubClient interface {
	AddLabel(org, repo string, number int, label string) error
	AssignIssue(org, repo string, number int, logins []string) error
	CreateComment(org, repo string, number int, comment string) error
	CreateFork(org, repo string) (string, error)
	CreatePullRequest(org, repo, title, body, head, base string, canModify bool) (int, error)
	CreateIssue(org, repo, title, body string, milestone int, labels, assignees []string) (int, error)
	EnsureFork(forkingUser, org, repo string) (string, error)
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
	GetPullRequestPatch(org, repo string, number int) ([]byte, error)
	GetPullRequests(org, repo string) ([]github.PullRequest, error)
	GetRepo(owner, name string) (github.FullRepo, error)
	IsMember(org, user string) (bool, error)
	ListIssueComments(org, repo string, number int) ([]github.IssueComment, error)
	GetIssueLabels(org, repo string, number int) ([]github.Label, error)
	ListOrgMembers(org, role string) ([]github.TeamMember, error)
}

// HelpProvider construct the pluginhelp.PluginHelp for this plugin.
func HelpProvider(_ []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	pluginHelp := &pluginhelp.PluginHelp{
		Description: `The chatgpt plugin is used for reviewing the PR with OpenAI`,
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/chatgpt [you question]",
		Description: "ask chatgpt for the PR. This command works both in PRs opened and updating events, also support comment on the opened PR.",
		Featured:    true,
		// depends on how the chatgpt plugin runs; needs auth by default (--allow-all=false)
		WhoCanUse: "Anyone",
		Examples:  []string{"/chatgpt could you help to review it?", "/chatgpt do you have any suggestions about this PR?"},
	})
	return pluginHelp, nil
}

// Server implements http.Handler. It validates incoming GitHub webhooks and
// then dispatches them to the appropriate plugins.
type Server struct {
	tokenGenerator func() []byte

	openaiClient  *openai.Client
	openaiModel   string
	systemMessage string

	ghc githubClient
	log *logrus.Entry
}

// ServeHTTP validates an incoming webhook and puts it into the event channel.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	eventType, eventGUID, payload, ok, _ := github.ValidateWebhook(w, r, s.tokenGenerator)
	if !ok {
		return
	}
	fmt.Fprint(w, "Event received. Have a nice day.")

	if err := s.handleEvent(eventType, eventGUID, payload); err != nil {
		logrus.WithError(err).Error("Error parsing event.")
	}
}

func (s *Server) handleEvent(eventType, eventGUID string, payload []byte) error {
	l := logrus.WithFields(logrus.Fields{
		"event-type":     eventType,
		github.EventGUID: eventGUID,
	})
	switch eventType {
	case "issue_comment":
		var ic github.IssueCommentEvent
		if err := json.Unmarshal(payload, &ic); err != nil {
			return err
		}
		go func() {
			if err := s.handleIssueComment(l, ic); err != nil {
				s.log.WithError(err).WithFields(l.Data).Info("chatgpt call failed.")
			}
		}()
	case "pull_request":
		var pr github.PullRequestEvent
		if err := json.Unmarshal(payload, &pr); err != nil {
			return err
		}
		go func() {
			if err := s.handlePullRequest(l, pr); err != nil {
				s.log.WithError(err).WithFields(l.Data).Info("chatgpt call failed.")
			}
		}()

	default:
		logrus.Debugf("skipping event of type %q", eventType)
	}
	return nil
}

func (s *Server) handlePullRequest(l *logrus.Entry, pre github.PullRequestEvent) error {
	// Only consider newly merged PRs
	if pre.Action != github.PullRequestActionOpened &&
		pre.Action != github.PullRequestActionSynchronize &&
		pre.Action != github.PullRequestActionReopened {
		return nil
	}

	pr := pre.PullRequest
	// Skip not mergable or draft PR.
	if pr.Draft || pr.Mergable != nil && !*pr.Mergable {
		return nil
	}

	org := pre.Repo.Owner.Login
	repo := pre.Repo.Name
	num := pre.Number

	// Do not create a new logger, its fields are re-used by the caller in case of errors
	*l = *l.WithFields(logrus.Fields{
		github.OrgLogField:  org,
		github.RepoLogField: repo,
		github.PrLogField:   num,
	})

	return s.handle(l, nil, &pr, "")
}

func (s *Server) handleIssueComment(l *logrus.Entry, ic github.IssueCommentEvent) error {
	// Only consider new comments in PRs.
	if !ic.Issue.IsPullRequest() || ic.Action != github.IssueCommentActionCreated {
		return nil
	}

	org := ic.Repo.Owner.Login
	repo := ic.Repo.Name
	num := ic.Issue.Number

	// Do not create a new logger, its fields are re-used by the caller in case of errors
	*l = *l.WithFields(logrus.Fields{
		github.OrgLogField:  org,
		github.RepoLogField: repo,
		github.PrLogField:   num,
	})

	pr, err := s.ghc.GetPullRequest(org, repo, num)
	if err != nil {
		return err
	}

	if pr.Mergable != nil && !*pr.Mergable {
		return s.createComment(l, org, repo, num, &ic.Comment, "I Skip the comment since it is not mergable.")
	}

	// Ignore comments that are not commands
	gptMatches := chatgptRe.FindAllStringSubmatch(ic.Comment.Body, -1)
	if len(gptMatches) == 0 || len(gptMatches[0]) != 2 {
		return nil
	}

	return s.handle(l, &ic.Comment, pr, gptMatches[0][1])
}

func (s *Server) handle(logger *logrus.Entry, comment *github.IssueComment, pr *github.PullRequest, foreword string) error {
	logger.Debug("call handle()")
	if foreword == "" {
		foreword = openaiQuestionForeword
	}

	org := pr.Base.Repo.Owner.Login
	repo := pr.Base.Repo.Name
	num := pr.Number

	patch, err := s.getPullRequestPatch(logger, org, repo, num)
	if err != nil {
		return err
	}
	if len(patch) > openaiMessageMaxLen {
		logger.Debugf("patch size is %d bytes", len(patch))
		logger.Debugf("patch content is: %s", patch)
		return s.createComment(logger, org, repo, num, comment, "I Skip it since changed size is too large")
	}

	message := strings.Join([]string{
		foreword,
		"This is the pr title:",
		"```text",
		pr.Title,

		"```",
		"These are the pr description:",
		"```text",
		pr.Body,
		"```",

		"This is the diff for the pull request:",
		"```diff",
		string(patch),
		"```",
	}, "\n")

	resp, err := s.sendMessageToChatGPTServer(logger, message)
	if err != nil {
		logger.Errorf("Failed to send message to OpenAI server: %v", err)
		return s.createComment(logger, org, repo, num, comment,
			"Sorry, failed to send message to OpenAI server!")
	}

	return s.createComment(logger, org, repo, num, comment, resp)
}

func (s *Server) getPullRequestPatch(l *logrus.Entry, org, repo string, num int) ([]byte, error) {
	patch, err := s.ghc.GetPullRequestPatch(org, repo, num)
	if err != nil {
		return nil, err
	}

	// when first opened. the patch content will be json info of the pull request.
	if patch[0] == '{' {
		l.Debug("got pr info in json format")
		time.Sleep(time.Second * 5)
		return s.getPullRequestPatch(l, org, repo, num)
	}

	return patch, nil
}

func (s *Server) createComment(l *logrus.Entry, org, repo string, num int, comment *github.IssueComment, resp string) error {
	if err := func() error {
		if comment != nil {
			return s.ghc.CreateComment(org, repo, num, plugins.FormatICResponse(*comment, "\n"+resp))
		}
		return s.ghc.CreateComment(org, repo, num, fmt.Sprintf("%s\n%s", botHeadnote, resp))
	}(); err != nil {
		l.WithError(err).Warn("failed to create comment")
		return err
	}

	logrus.Debug("Created comment")
	return nil
}

func (s *Server) sendMessageToChatGPTServer(logger *logrus.Entry, message string) (string, error) {
	resp, err := s.openaiClient.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: s.openaiModel,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: s.systemMessage,
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: message,
				},
			},
		},
	)

	if err != nil {
		return "", fmt.Errorf("ChatCompletion error: %w", err)
	}

	logger.WithFields(logrus.Fields{
		"model":             resp.Model,
		"total_tokens":      resp.Usage.TotalTokens,
		"completion_tokens": resp.Usage.CompletionTokens,
		"prompt_tokens":     resp.Usage.PromptTokens,
	}).Debug("openai token usage.")

	if len(resp.Choices) != 0 {
		return resp.Choices[len(resp.Choices)-1].Message.Content, nil
	}

	return "", nil
}
