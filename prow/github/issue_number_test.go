package github

import (
	"reflect"
	"testing"
)

func TestNormalizeIssueNumbers(t *testing.T) {
	testcases := []struct {
		name     string
		content  string
		currOrg  string
		currRepo string

		expectNumbers []IssueNumberData
	}{
		{
			name:     "issue number with short prefix",
			content:  "Issue Number: close #123",
			currOrg:  "pingcap",
			currRepo: "tidb",

			expectNumbers: []IssueNumberData{
				{AssociatePrefix: "close", Org: "pingcap", Repo: "tidb", Number: 123},
			},
		},
		{
			name:     "issue number with full prefix in the same Repo",
			content:  "Issue Number: close pingcap/tidb#123",
			currOrg:  "pingcap",
			currRepo: "tidb",

			expectNumbers: []IssueNumberData{
				{AssociatePrefix: "close", Org: "pingcap", Repo: "tidb", Number: 123},
			},
		},
		{
			name:     "issue number with full prefix in the another Repo",
			content:  "Issue Number: close pingcap/tiflow#123",
			currOrg:  "pingcap",
			currRepo: "tidb",

			expectNumbers: []IssueNumberData{
				{AssociatePrefix: "close", Org: "pingcap", Repo: "tiflow", Number: 123},
			},
		},
		{
			name:     "issue Number with full prefix in the another Org",
			content:  "Issue Number: close tikv/tikv#123",
			currOrg:  "pingcap",
			currRepo: "tidb",

			expectNumbers: []IssueNumberData{
				{AssociatePrefix: "close", Org: "tikv", Repo: "tikv", Number: 123},
			},
		},
		{
			name:     "issue Number with link prefix in the same Repo",
			content:  "Issue Number: close https://github.com/pingcap/tidb/issues/123",
			currOrg:  "pingcap",
			currRepo: "tidb",

			expectNumbers: []IssueNumberData{
				{AssociatePrefix: "close", Org: "pingcap", Repo: "tidb", Number: 123},
			},
		},
		{
			name:     "issue Number with link prefix in the another Repo",
			content:  "Issue Number: close https://github.com/pingcap/tiflow/issues/123",
			currOrg:  "pingcap",
			currRepo: "tidb",

			expectNumbers: []IssueNumberData{
				{AssociatePrefix: "close", Org: "pingcap", Repo: "tiflow", Number: 123},
			},
		},
		{
			name:     "issue Number with link prefix in the another Org",
			content:  "Issue Number: close https://github.com/tikv/tikv/issues/123",
			currOrg:  "pingcap",
			currRepo: "tidb",

			expectNumbers: []IssueNumberData{
				{AssociatePrefix: "close", Org: "tikv", Repo: "tikv", Number: 123},
			},
		},
		{
			name:     "duplicate issue numbers with same associate prefix",
			content:  "Issue Number: close #123, close https://github.com/pingcap/tidb/issues/123",
			currOrg:  "pingcap",
			currRepo: "tidb",

			expectNumbers: []IssueNumberData{
				{AssociatePrefix: "close", Org: "pingcap", Repo: "tidb", Number: 123},
			},
		},
		{
			name:     "multiple issue numbers with same associate prefix",
			content:  "Issue Number: close #456, close https://github.com/pingcap/tidb/issues/123",
			currOrg:  "pingcap",
			currRepo: "tidb",

			expectNumbers: []IssueNumberData{
				{AssociatePrefix: "close", Org: "pingcap", Repo: "tidb", Number: 123},
				{AssociatePrefix: "close", Org: "pingcap", Repo: "tidb", Number: 456},
			},
		},
		{
			name:     "multiple issue numbers break with newline",
			content:  "Issue Number: ref #123\nclose #456",
			currOrg:  "pingcap",
			currRepo: "tidb",

			expectNumbers: []IssueNumberData{
				{AssociatePrefix: "ref", Org: "pingcap", Repo: "tidb", Number: 123},
				{AssociatePrefix: "close", Org: "pingcap", Repo: "tidb", Number: 456},
			},
		},
	}

	for _, testcase := range testcases {
		tc := testcase
		actualNumbers := NormalizeIssueNumbers(tc.content, tc.currOrg, tc.currRepo)

		if !reflect.DeepEqual(tc.expectNumbers, actualNumbers) {
			t.Errorf("For case \"%s\": \nexpect issue numbers are: \n%v\nbut got: \n%v",
				tc.name, tc.expectNumbers, actualNumbers)
		}
	}
}
