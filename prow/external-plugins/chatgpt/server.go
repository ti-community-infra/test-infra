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
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"regexp"
	"strings"

	"github.com/sirupsen/logrus"

	"k8s.io/test-infra/prow/config"
	"k8s.io/test-infra/prow/git/v2"
	"k8s.io/test-infra/prow/github"
	"k8s.io/test-infra/prow/pluginhelp"
	"k8s.io/test-infra/prow/plugins"
)

const pluginName = "chatgpt"

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
		Description: `The chatgpt plugin is used for chatgpting PRs across branches. For every successful chatgpt invocation a new PR is opened against the target branch and assigned to the requestor. If the parent PR contains a release note, it is copied to the chatgpt PR.`,
	}
	pluginHelp.AddCommand(pluginhelp.Command{
		Usage:       "/chatgpt [branch]",
		Description: "chatgpt a PR to a different branch. This command works both in merged PRs (the chatgpt PR is opened immediately) and open PRs (the chatgpt PR opens as soon as the original PR merges).",
		Featured:    true,
		// depends on how the chatgpt server runs; needs auth by default (--allow-all=false)
		WhoCanUse: "Members of the trusted organization for the repo.",
		Examples:  []string{"/chatgpt release-3.9", "/chatgpt release-1.15"},
	})
	return pluginHelp, nil
}

// Server implements http.Handler. It validates incoming GitHub webhooks and
// then dispatches them to the appropriate plugins.
type Server struct {
	tokenGenerator  func() []byte
	chatGPTServer   string
	chatGPTAPIToken string

	gc  git.ClientFactory
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
	if pr.Mergable != nil && !*pr.Mergable {
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

	return s.handle(l, nil, org, repo, pr.Title, pr.Body, num)
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

	// Ignore comments that are not commands
	gptMatches := chatgptRe.FindAllStringSubmatch(ic.Comment.Body, -1)
	if len(gptMatches) == 0 || len(gptMatches[0]) != 2 {
		return nil
	}

	pr, err := s.ghc.GetPullRequest(org, repo, num)
	if err != nil {
		return fmt.Errorf("failed to get pull request %s/%s#%d: %w", org, repo, num, err)
	}

	return s.handle(l, &ic.Comment, org, repo, pr.Title, pr.Body, num)
}

func (s *Server) handle(logger *logrus.Entry, comment *github.IssueComment, org, repo, title, body string, num int) error {
	diff, err := s.ghc.GetPullRequestPatch(org, repo, num)
	if err != nil {
		logger.Errorf("Failed to get PR diff: %v", err)
		return err
	}

	resp, err := s.sendMessageToChatGPTServer(title, body, diff)
	if err != nil {
		logger.Errorf("Failed to send message to ChatGPT server: %v", err)
		return err
	}

	return s.createComment(logger, org, repo, num, comment, resp)
}

func (s *Server) createComment(l *logrus.Entry, org, repo string, num int, comment *github.IssueComment, resp string) error {
	if err := func() error {
		if comment != nil {
			return s.ghc.CreateComment(org, repo, num, plugins.FormatICResponse(*comment, resp))
		}
		return s.ghc.CreateComment(org, repo, num, fmt.Sprintf("In response to a chatgpt label: %s", resp))
	}(); err != nil {
		l.WithError(err).Warn("failed to create comment")
		return err
	}

	logrus.Debug("Created comment")
	return nil
}

func (s *Server) sendMessageToChatGPTServer(title, desc string, diff []byte) (string, error) {
	type payload struct {
		Title string `json:"title"`
		Diff  []byte `json:"diff"`
	}

	p := payload{
		Title: title,
		Diff:  diff,
	}

	pBytes, err := json.Marshal(p)
	if err != nil {
		return "", err
	}

	resp, err := http.Post(s.chatGPTServer, "application/json", strings.NewReader(string(pBytes)))
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	respBytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	var response map[string]interface{}
	if err := json.Unmarshal(respBytes, &response); err != nil {
		return "", err
	}

	return response["response"].(string), nil
}
