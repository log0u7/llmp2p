package ref

import "testing"

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    *Ref
		wantErr bool
	}{
		{
			name: "bare repo",
			in:   "hf:Qwen/Qwen3-Coder-30B-A3B-GGUF",
			want: &Ref{Owner: "Qwen", Repo: "Qwen3-Coder-30B-A3B-GGUF", Revision: "main"},
		},
		{
			name: "double slash prefix",
			in:   "hf://meta-llama/Llama-3.2-1B",
			want: &Ref{Owner: "meta-llama", Repo: "Llama-3.2-1B", Revision: "main"},
		},
		{
			name: "with revision",
			in:   "hf:Qwen/Qwen3@refs/pr/1",
			want: &Ref{Owner: "Qwen", Repo: "Qwen3", Revision: "refs/pr/1"},
		},
		{
			name: "with revision and path",
			in:   "hf:Qwen/Qwen3@1a2b3c#model.gguf",
			want: &Ref{Owner: "Qwen", Repo: "Qwen3", Revision: "1a2b3c", Path: "model.gguf"},
		},
		{
			name: "slash revision only",
			in:   "hf:Qwen/Qwen3@refs/pr/12",
			want: &Ref{Owner: "Qwen", Repo: "Qwen3", Revision: "refs/pr/12"},
		},
		{
			name: "nested artifact path",
			in:   "hf:org/repo#sub/dir/file.gguf",
			want: &Ref{Owner: "org", Repo: "repo", Revision: "main", Path: "sub/dir/file.gguf"},
		},
		{name: "missing prefix", in: "Qwen/Qwen3", wantErr: true},
		{name: "no slash", in: "hf:Qwen", wantErr: true},
		{name: "empty repo", in: "hf:Qwen/", wantErr: true},
		{name: "empty owner", in: "hf:/repo", wantErr: true},
		{name: "empty revision", in: "hf:Qwen/Qwen3@", wantErr: true},
		{name: "traversal in revision", in: "hf:Qwen/Qwen3@../etc", wantErr: true},
		{name: "absolute artifact path", in: "hf:Qwen/Qwen3#/etc/passwd", wantErr: true},
		{name: "traversal in artifact path", in: "hf:Qwen/Qwen3#../../etc/passwd", wantErr: true},
		{name: "empty path segment", in: "hf:Qwen/Qwen3#dir//file.gguf", wantErr: true},
		{name: "bad path chars", in: "hf:Qwen/Qwen3#dir/file;.gguf", wantErr: true},
		{name: "space in owner", in: "hf:qw en/repo", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Parse(%q) error = nil, want error", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.in, err)
			}
			if got.Owner != tt.want.Owner || got.Repo != tt.want.Repo ||
				got.Revision != tt.want.Revision || got.Path != tt.want.Path {
				t.Fatalf("Parse(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
		})
	}
}

func TestRefIDAndString(t *testing.T) {
	r, err := Parse("hf:Qwen/Qwen3@dev#file.gguf")
	if err != nil {
		t.Fatal(err)
	}
	if got := r.ID(); got != "Qwen/Qwen3" {
		t.Fatalf("ID() = %q, want %q", got, "Qwen/Qwen3")
	}
	if got, want := r.String(), "hf:Qwen/Qwen3@dev#file.gguf"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}

	r, err = Parse("hf:Qwen/Qwen3")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := r.String(), "hf:Qwen/Qwen3"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestParseRoundTrip(t *testing.T) {
	for _, in := range []string{
		"hf:Qwen/Qwen3",
		"hf:Qwen/Qwen3@refs/pr/12",
		"hf:Qwen/Qwen3#file.gguf",
		"hf:Qwen/Qwen3@1a2b3c#sub/file.gguf",
	} {
		r, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", in, err)
		}
		r2, err := Parse(r.String())
		if err != nil {
			t.Fatalf("Parse(%q) round trip: %v", r.String(), err)
		}
		if r2.String() != r.String() {
			t.Fatalf("round trip mismatch: %q -> %q", r.String(), r2.String())
		}
	}
}
