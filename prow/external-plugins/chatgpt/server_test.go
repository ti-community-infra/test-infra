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
	_ "embed"
	"testing"

	"k8s.io/test-infra/prow/github"
)

func Test_shouldRunTaskForPR(t *testing.T) {
	tests := []struct {
		name string
		task *Task
		pr   *github.PullRequest
		want bool
	}{
		{
			name: "always run is false",
			task: &Task{
				AlwaysRun: false,
			},
			pr:   &github.PullRequest{},
			want: false,
		},
		{
			name: "all skippings missed",
			task: &Task{
				AlwaysRun:   true,
				SkipAuthors: []string{"user1", "user2"},
				SkipBrancheRegs: []string{
					`^main$`,
					`^release-.*$`,
				},
				SkipLabelRegs: []string{
					`^do-not-merge$`,
					`^wip$`,
				},
			},
			pr: &github.PullRequest{
				User: github.User{
					Login: "user3",
				},
				Base: github.PullRequestBranch{
					Ref: "feature-abc",
				},
				Labels: []github.Label{
					{
						Name: "enhancement",
					},
				},
			},
			want: true,
		},
		{
			name: "skip by author",
			task: &Task{
				AlwaysRun:   true,
				SkipAuthors: []string{"user1", "user2"},
				SkipBrancheRegs: []string{
					`^main$`,
					`^release-.*$`,
				},
				SkipLabelRegs: []string{
					`^do-not-merge$`,
					`^wip$`,
				},
			},
			pr: &github.PullRequest{
				User: github.User{
					Login: "user1",
				},
				Base: github.PullRequestBranch{
					Ref: "feature-abc",
				},
				Labels: []github.Label{
					{
						Name: "enhancement",
					},
				},
			},
			want: false,
		},
		{
			name: "skip by label",
			task: &Task{
				AlwaysRun:   true,
				SkipAuthors: []string{"user1", "user2"},
				SkipBrancheRegs: []string{
					`^main$`,
					`^release-.*$`,
				},
				SkipLabelRegs: []string{
					`^do-not-merge$`,
					`^wip$`,
				},
			},
			pr: &github.PullRequest{
				User: github.User{
					Login: "user3",
				},
				Base: github.PullRequestBranch{
					Ref: "feature-branch",
				},
				Labels: []github.Label{
					{
						Name: "bug",
					},
					{
						Name: "do-not-merge",
					},
				},
			},
			want: false,
		},
		{
			name: "skip by branch",
			task: &Task{
				AlwaysRun:   true,
				SkipAuthors: []string{"user1", "user2"},
				SkipBrancheRegs: []string{
					`^main$`,
					`^release-.*$`,
				},
				SkipLabelRegs: []string{
					`^do-not-merge$`,
					`^wip$`,
				},
			},
			pr: &github.PullRequest{
				User: github.User{
					Login: "user4",
				},
				Base: github.PullRequestBranch{
					Ref: "release-v1.0",
				},
				Labels: []github.Label{
					{
						Name: "type/bug",
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = tt.task.initRegexps()
			if got := shouldRunTaskForPR(tt.task, tt.pr); got != tt.want {
				t.Errorf("shouldRunTaskForPR() = %v, want %v", got, tt.want)
			}
		})
	}
}
