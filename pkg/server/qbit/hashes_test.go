package qbit

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestSplitHashes(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   []string
	}{
		{"none", nil, nil},
		{"single", []string{"abc"}, []string{"abc"}},
		{"pipe joined", []string{"abc|def"}, []string{"abc", "def"}},
		{"repeated values", []string{"abc", "def"}, []string{"abc", "def"}},
		{"spaces and empty parts", []string{" abc | |def|", ""}, []string{"abc", "def"}},
		{"all sentinel", []string{"all"}, []string{"all"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := splitHashes(tt.values); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("splitHashes(%q) = %q, want %q", tt.values, got, tt.want)
			}
		})
	}
}

func TestHashesContextSplitsPipeJoinedFormValue(t *testing.T) {
	form := url.Values{"hashes": {"abc|def"}, "deleteFiles": {"true"}}
	req := httptest.NewRequest(http.MethodPost, "/torrents/delete", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var got []string
	hashesContext(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = getHashes(r.Context())
	})).ServeHTTP(httptest.NewRecorder(), req)

	if want := []string{"abc", "def"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("hashes = %q, want %q", got, want)
	}
}
