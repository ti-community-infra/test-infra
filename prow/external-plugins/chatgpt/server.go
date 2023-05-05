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
	pluginName              = "chatgpt"
	gitHostBaseURL          = "https://github.com"
	openaiMessageMaxLen     = 9000
	defaultIssueReviewWorld = "default"
)

type githubClient interface {
	AddLabel(org, repo string, number int, label string) error
	AssignIssue(org, repo string, number int, logins []string) error
	CreateComment(org, repo string, number int, comment string) error
	CreateFork(org, repo string) (string, error)
	CreatePullRequest(org, repo, title, body, head, base string, canModify bool) (int, error)
	CreateIssue(org, repo, title, body string, milestone int, labels, assignees []string) (int, error)
	EnsureFork(forkingUser, org, repo string) (string, error)
	GetPullRequest(org, repo string, number int) (*github.PullRequest, error)
	GetPullRequestDiff(org, repo string, number int) ([]byte, error)
	GetPullRequests(org, repo string) ([]github.PullRequest, error)
	GetRepo(owner, name string) (github.FullRepo, error)
	IsMember(org, user string) (bool, error)
	ListIssueComments(org, repo string, number int) ([]github.IssueComment, error)
	GetIssueLabels(org, repo string, number int) ([]github.Label, error)
	ListOrgMembers(org, role string) ([]github.TeamMember, error)
}

// HelpProvider construct the pluginhelp.PluginHelp for this plugin.
func HelpProviderFactory(command string) func(_ []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
	return func(_ []config.OrgRepo) (*pluginhelp.PluginHelp, error) {
		pluginHelp := &pluginhelp.PluginHelp{
			Description: `The chatgpt plugin is used for reviewing the PR with OpenAI`,
		}
		pluginHelp.AddCommand(pluginhelp.Command{
			Usage:       fmt.Sprintf("/%s [you question]", command),
			Description: "ask chatgpt for the PR. This command works both in PRs opened and updating events, also support comment on the opened PR.",
			Featured:    true,
			// depends on how the plugin runs; needs auth by default (--allow-all=false)
			WhoCanUse: "Anyone",
			Examples: []string{
				fmt.Sprintf("/%s default", command),
				fmt.Sprintf("/%s do you have any suggestions about this PR?", command),
			},
		})
		return pluginHelp, nil
	}
}

// Server implements http.Handler. It validates incoming GitHub webhooks and
// then dispatches them to the appropriate plugins.
type Server struct {
	tokenGenerator func() []byte

	openaiModel            string
	openaiClient           *openai.Client
	openaiTaskAgent        *ConfigAgent
	issueCommentMatchRegex *regexp.Regexp

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

	return s.handle(l, &pr, nil, "")
}

func (s *Server) handleIssueComment(l *logrus.Entry, ic github.IssueCommentEvent) error {
	// Only consider new comments in PRs.
	if !ic.Issue.IsPullRequest() || ic.Action != github.IssueCommentActionCreated {
		return nil
	}

	// Ignore comments that are not commands
	commentMatches := s.issueCommentMatchRegex.FindAllStringSubmatch(ic.Comment.Body, -1)
	if len(commentMatches) == 0 || len(commentMatches[0]) != 2 {
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

	foreword := commentMatches[0][1]
	if foreword == defaultIssueReviewWorld {
		foreword = defaultPromte
	}

	return s.handle(l, pr, &ic.Comment, foreword)
}

func (s *Server) handle(logger *logrus.Entry, pr *github.PullRequest, comment *github.IssueComment, foreword string) error {
	org := pr.Base.Repo.Owner.Login
	repo := pr.Base.Repo.Name
	num := pr.Number

	logger.Debug("start handle...")
	diff, err := s.getPullRequestDiff(logger, org, repo, num)
	if err != nil {
		return err
	}
	if len(diff) > openaiMessageMaxLen {
		logger.Debugf("diff size is %d bytes", len(diff))
		logger.Debugf("diff content is: %s", diff)
		return s.createComment(logger, org, repo, num, comment, "I Skip it since changed size is too large")
	}

	tasks, err := s.getTasks(org, repo, foreword)
	if err != nil {
		logger.WithError(err).Error("Failed to get tasks")
		return err
	}

	for n, task := range tasks {
		if err := s.taskRun(logger.WithField("ai-task", n), &task, pr, string(diff), comment); err != nil {
			return err
		}
	}

	return nil
}

func (s *Server) getTasks(org, repo, foreword string) (map[string]TaskConfig, error) {
	if foreword == "" {
		return s.openaiTaskAgent.TasksFor(org, repo)
	}

	tasks := map[string]TaskConfig{
		"user-comment-trigger": {
			SystemMessage:        defaultSystemMessage,
			UserPrompt:           foreword,
			PatchIntroducePrompt: defaultPrPatchIntroducePromte,
		},
	}
	return tasks, nil
}

func (s *Server) getPullRequestDiff(l *logrus.Entry, org, repo string, num int) ([]byte, error) {
	diff, err := s.ghc.GetPullRequestDiff(org, repo, num)
	if err != nil {
		return nil, err
	}

	// when first opened. the patch content will be json info of the pull request.
	if diff[0] == '{' {
		l.Debug("got pr info in json format")
		time.Sleep(time.Second * 5)
		return s.getPullRequestDiff(l, org, repo, num)
	}

	return diff, nil
}

func (s *Server) taskRun(logger *logrus.Entry, task *TaskConfig, pr *github.PullRequest, patch string, comment *github.IssueComment) error {
	logger.Debugf("start deal task %s...", task.Name)
	message := strings.Join([]string{
		task.UserPrompt,
		"This is the pr title:",
		"```text",
		pr.Title,

		"```",
		"These are the pr description:",
		"```text",
		pr.Body,
		"```",
		task.PatchIntroducePrompt,
		"```diff",
		patch,
		"```",
	}, "\n")

	resp, err := s.sendMessageToChatGPTServer(logger, task.SystemMessage, message)
	if err != nil {
		logger.Errorf("Failed to send message to OpenAI server: %v", err)
		return s.createComment(logger, pr.Base.Repo.Owner.Login, pr.Base.Repo.Name, pr.Number, comment,
			"Sorry, failed to send message to OpenAI server!")
	}

	if task.OutputStaticHeadNote != "" {
		resp = fmt.Sprintf("%s\n%s", task.OutputStaticHeadNote, resp)
	}
	return s.createComment(logger, pr.Base.Repo.Owner.Login, pr.Base.Repo.Name, pr.Number, comment, resp)
}

func (s *Server) createComment(l *logrus.Entry, org, repo string, num int, comment *github.IssueComment, resp string) error {
	if err := func() error {
		if comment != nil {
			return s.ghc.CreateComment(org, repo, num, plugins.FormatICResponse(*comment, "\n"+resp))
		}
		return s.ghc.CreateComment(org, repo, num, resp)
	}(); err != nil {
		l.WithError(err).Warn("failed to create comment")
		return err
	}

	logrus.Debug("Created comment")
	return nil
}

func (s *Server) sendMessageToChatGPTServer(logger *logrus.Entry, systemMessage, userMessage string) (string, error) {
	resp, err := s.openaiClient.CreateChatCompletion(
		context.Background(),
		openai.ChatCompletionRequest{
			Model: s.openaiModel,
			Messages: []openai.ChatCompletionMessage{
				{
					Role:    openai.ChatMessageRoleSystem,
					Content: systemMessage,
				},
				{
					Role:    openai.ChatMessageRoleUser,
					Content: userMessage,
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
