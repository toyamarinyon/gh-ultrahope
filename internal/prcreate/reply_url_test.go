package prcreate

import "testing"

func TestParsePRCommentURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		raw       string
		wantOK    bool
		wantOwner string
		wantRepo  string
		wantPR    int
		wantKind  commentKind
		wantID    int64
	}{
		{
			name:      "discussion_r",
			raw:       "https://github.com/giselles-ai/giselle/pull/2605#discussion_r123456",
			wantOK:    true,
			wantOwner: "giselles-ai",
			wantRepo:  "giselle",
			wantPR:    2605,
			wantKind:  commentKindReviewComment,
			wantID:    123456,
		},
		{
			name:      "short_r_on_files",
			raw:       "https://github.com/giselles-ai/giselle/pull/2605/files#r999",
			wantOK:    true,
			wantOwner: "giselles-ai",
			wantRepo:  "giselle",
			wantPR:    2605,
			wantKind:  commentKindReviewComment,
			wantID:    999,
		},
		{
			name:      "issuecomment",
			raw:       "https://github.com/giselles-ai/giselle/pull/2605#issuecomment-42",
			wantOK:    true,
			wantOwner: "giselles-ai",
			wantRepo:  "giselle",
			wantPR:    2605,
			wantKind:  commentKindIssueComment,
			wantID:    42,
		},
		{
			name:   "missing_fragment",
			raw:    "https://github.com/giselles-ai/giselle/pull/2605",
			wantOK: false,
		},
		{
			name:   "invalid_path",
			raw:    "https://github.com/giselles-ai/giselle/issues/1#issuecomment-2",
			wantOK: false,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parsePRCommentURL(tc.raw)
			if tc.wantOK {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got.Owner != tc.wantOwner || got.Repo != tc.wantRepo || got.PRNumber != tc.wantPR || got.Kind != tc.wantKind || got.CommentID != tc.wantID {
					t.Fatalf("unexpected parse result: %+v", got)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error, got nil: %+v", got)
				}
			}
		})
	}
}
