package github

import (
	_ "embed"
	"reflect"
	"testing"
)

//go:embed issue_number_bug.dat
var content string

func TestNormalizeIssueNumbers(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		currOrg  string
		currRepo string
		want     []IssueNumberData
	}{
		{
			name:     "xxx",
			content:  content,
			currOrg:  "tikv",
			currRepo: "tikv",
			want: []IssueNumberData{{
				AssociatePrefix: "ref",
				Org:             "tikv",
				Repo:            "tikv",
				Number:          14570,
			}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeIssueNumbers(tt.content, tt.currOrg, tt.currRepo); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("NormalizeIssueNumbers() = %v, want %v", got, tt.want)
			}
		})
	}
}
