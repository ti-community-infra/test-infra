package github

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestNormalizeSignedOff(t *testing.T) {
	testcases := []struct {
		name           string
		commitMessages []string

		expectSignedAuthors []SignedAuthor
	}{
		{
			name: "single commit message with signed-off",
			commitMessages: []string{
				"commit message headline 1\n\nSigned-off-by: foo <foo.bar@gmail.com>",
			},

			expectSignedAuthors: []SignedAuthor{
				{
					Name:  "foo",
					Email: "foo.bar@gmail.com",
				},
			},
		},
		{
			name: "single commit message without signed-off",
			commitMessages: []string{
				"commit message headline 1\n\n",
			},

			expectSignedAuthors: []SignedAuthor{},
		},
		{
			name: "multiple commit messages with same signed-off",
			commitMessages: []string{
				"commit message headline 1\n\nSigned-off-by: foo <foo.bar@gmail.com>",
				"commit message headline 2\n\nSigned-off-by: foo <foo.bar@gmail.com>",
			},

			expectSignedAuthors: []SignedAuthor{
				{
					Name:  "foo",
					Email: "foo.bar@gmail.com",
				},
			},
		},
		{
			name: "multiple commit messages with different signed-off",
			commitMessages: []string{
				"commit message headline 1\n\nSigned-off-by: foo <foo.bar@gmail.com>",
				"commit message headline 2\n\nSigned-off-by: baz <baz.qux@gmail.com>",
				"commit message headline 3\n\nSigned-off-by: foo <foo.bar@gmail.com>",
			},

			expectSignedAuthors: []SignedAuthor{
				{
					Name:  "foo",
					Email: "foo.bar@gmail.com",
				},
				{
					Name:  "baz",
					Email: "baz.qux@gmail.com",
				},
			},
		},
		{
			name: "single commit message with multiple signed-off",
			commitMessages: []string{
				"commit message headline 1\n\nSigned-off-by: foo <foo.bar@gmail.com>\n\nSigned-off-by: baz <baz.qux@gmail.com>",
			},

			expectSignedAuthors: []SignedAuthor{
				{
					Name:  "foo",
					Email: "foo.bar@gmail.com",
				},
				{
					Name:  "baz",
					Email: "baz.qux@gmail.com",
				},
			},
		},
	}

	for _, testcase := range testcases {
		tc := testcase
		actualSignedAuthors := NormalizeSignedOffBy(tc.commitMessages)

		sortSignedAuthors(actualSignedAuthors)
		sortSignedAuthors(tc.expectSignedAuthors)

		if !reflect.DeepEqual(actualSignedAuthors, tc.expectSignedAuthors) {
			t.Errorf("For case \"%s\": \nexpect signed authors are: \n%+v\nbut got: \n%+v",
				tc.name, tc.expectSignedAuthors, actualSignedAuthors)
		}
	}
}

func sortSignedAuthors(signs []SignedAuthor) {
	sort.Slice(signs, func(i, j int) bool {
		compare := strings.Compare(signs[i].Name, signs[j].Name)
		if compare == 0 {
			return strings.Compare(signs[i].Email, signs[j].Email) < 0
		}
		return compare < 0
	})
}

func TestNormalizeCoAuthorBy(t *testing.T) {
	testLogin1 := "login1"
	testLogin2 := "login2"
	testLogin3 := "login3"

	testcases := []struct {
		name           string
		commitAuthors  []CommitAuthor
		commitMessages []string
		prAuthorLogin  string

		expectCoAuthors []CoAuthor
	}{
		{
			name: "all commits are from the PR author",
			commitAuthors: []CommitAuthor{
				{
					Name:  "zhangsan",
					Email: "zhangsan@gmail.com",
					Login: &testLogin1,
				},
				{
					Name:  "zhangsan",
					Email: "zhangsan@gmail.com",
					Login: &testLogin1,
				},
			},
			commitMessages: []string{
				"commit message headline 1\n\nSigned-off-by: zhangsan <zhangsan@gmail.com>",
				"commit message headline 2\n\nSigned-off-by: zhangsan <zhangsan@gmail.com>",
			},
			prAuthorLogin: testLogin1,

			expectCoAuthors: []CoAuthor{},
		},
		{
			name: "one commit submitted by a non-author of the PR",
			commitAuthors: []CommitAuthor{
				{
					Name:  "zhangsan",
					Email: "zhangsan@gmail.com",
					Login: &testLogin1,
				},
				{
					Name:  "lisi",
					Email: "lisi@gmail.com",
					Login: &testLogin2,
				},
			},
			commitMessages: []string{
				"commit message headline 1\n\nSigned-off-by: zhangsan <zhangsan@gmail.com>",
				"commit message headline 2\n\nSigned-off-by: lisi <lisi@gmail.com>\nCo-authored-by: lisi <lisi@gmail.com>",
			},
			prAuthorLogin: testLogin1,

			expectCoAuthors: []CoAuthor{
				{
					Name:  "lisi",
					Email: "lisi@gmail.com",
				},
			},
		},
		{
			name: "two commits submitted by one non-author of the PR",
			commitAuthors: []CommitAuthor{
				{
					Name:  "zhangsan",
					Email: "zhangsan@gmail.com",
					Login: &testLogin1,
				},
				{
					Name:  "lisi",
					Email: "lisi@gmail.com",
					Login: &testLogin2,
				},
				{
					Name:  "lisi",
					Email: "lisi@gmail.com",
					Login: &testLogin2,
				},
			},
			commitMessages: []string{
				"commit message headline 1\n\nSigned-off-by: zhangsan <zhangsan@gmail.com>",
				"commit message headline 2\n\nSigned-off-by: lisi <lisi@gmail.com>\nCo-authored-by: lisi <lisi@gmail.com>",
				"commit message headline 3\n\nSigned-off-by: lisi <lisi@gmail.com>\nCo-authored-by: lisi <lisi@gmail.com>",
			},
			prAuthorLogin: testLogin1,

			expectCoAuthors: []CoAuthor{
				{
					Name:  "lisi",
					Email: "lisi@gmail.com",
				},
			},
		},
		{
			name: "two commits submitted by two non-author of the PR",
			commitAuthors: []CommitAuthor{
				{
					Name:  "zhangsan",
					Email: "zhangsan@gmail.com",
					Login: &testLogin1,
				},
				{
					Name:  "lisi",
					Email: "lisi@gmail.com",
					Login: &testLogin2,
				},
				{
					Name:  "wangwu",
					Email: "wangwu@gmail.com",
					Login: &testLogin3,
				},
			},
			commitMessages: []string{
				"commit message headline 1\n\nSigned-off-by: zhangsan <zhangsan@gmail.com>",
				"commit message headline 2\n\nSigned-off-by: lisi <lisi@gmail.com>\nCo-authored-by: lisi <lisi@gmail.com>",
				"commit message headline 3\n\nSigned-off-by: wangwu <wangwu@gmail.com>\nCo-authored-by: wangwu <wangwu@gmail.com>",
			},
			prAuthorLogin: testLogin1,

			expectCoAuthors: []CoAuthor{
				{
					Name:  "lisi",
					Email: "lisi@gmail.com",
				},
				{
					Name:  "wangwu",
					Email: "wangwu@gmail.com",
				},
			},
		},
		{
			name: "two commits submitted by two non-author of the PR",
			commitAuthors: []CommitAuthor{
				{
					Name:  "zhangsan",
					Email: "zhangsan@gmail.com",
				},
				{
					Name:  "zhangsan",
					Email: "zhangsan@gmail.com",
				},
			},
			commitMessages: []string{
				"commit message headline 1\n\nSigned-off-by: zhangsan <zhangsan@gmail.com>\nCo-authored-by: zhangsan <zhangsan@gmail.com>",
				"commit message headline 2\n\nSigned-off-by: zhangsan <zhangsan@gmail.com>\nCo-authored-by: zhangsan <zhangsan@gmail.com>",
			},
			prAuthorLogin: testLogin1,

			expectCoAuthors: []CoAuthor{
				{
					Name:  "zhangsan",
					Email: "zhangsan@gmail.com",
				},
			},
		},
		{
			name: "one commit suggested by co-another but another by pr author",
			commitAuthors: []CommitAuthor{
				{
					Name:  "zhangsan",
					Email: "zhangsan@gmail.com",
					Login: &testLogin1,
				},
				{
					Name:  "zhangsan",
					Email: "zhangsan@gmail.com",
					Login: &testLogin1,
				},
			},
			commitMessages: []string{
				"commit message headline 1\n\nSigned-off-by: zhangsan <zhangsan@gmail.com>",
				"commit message headline 2\n\nSigned-off-by: zhangsan <zhangsan@gmail.com>\nCo-authored-by: lisi <lisi@gmail.com>",
			},
			prAuthorLogin: testLogin1,

			expectCoAuthors: []CoAuthor{
				{
					Name:  "lisi",
					Email: "lisi@gmail.com",
				},
			},
		},
	}

	for _, testcase := range testcases {
		tc := testcase
		actualCoAuthors := NormalizeCoAuthorBy(tc.commitAuthors, tc.commitMessages, tc.prAuthorLogin)

		sortCoAuthors(actualCoAuthors)
		sortCoAuthors(tc.expectCoAuthors)

		if !reflect.DeepEqual(actualCoAuthors, tc.expectCoAuthors) {
			t.Errorf("For case \"%s\": \nexpect co-authors are: \n%+v\nbut got: \n%+v",
				tc.name, tc.expectCoAuthors, actualCoAuthors)
		}
	}
}

func sortCoAuthors(coAuthors []CoAuthor) {
	sort.Slice(coAuthors, func(i, j int) bool {
		compare := strings.Compare(coAuthors[i].Name, coAuthors[j].Name)
		if compare == 0 {
			return strings.Compare(coAuthors[i].Email, coAuthors[j].Email) < 0
		}
		return compare < 0
	})
}
