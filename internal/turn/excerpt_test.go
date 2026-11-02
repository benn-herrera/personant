package turn

import "testing"

func TestRenderTurnExcerpt(t *testing.T) {
	const response = "*topic: thr_1 [a, b, c, d]*\nReply.\n"
	tests := []struct {
		name      string
		fileLines []string
		want      string
	}{
		{
			name: "no file events",
			want: "## Turn 3 · 2026-05-18T12:00:00Z · [a, b]\n\n**user:** ask\n\n**agent:** Reply.\n",
		},
		{
			name: "file events between header and user",
			fileLines: []string{
				"[file read #a5e11e51f3c8] src/a.go",
				"[file commit @deadbee] src/a.go",
			},
			want: "## Turn 3 · 2026-05-18T12:00:00Z · [a, b]\n\n" +
				"[file read #a5e11e51f3c8] src/a.go\n" +
				"[file commit @deadbee] src/a.go\n\n" +
				"**user:** ask\n\n**agent:** Reply.\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderTurnExcerpt(3, "2026-05-18T12:00:00Z", []string{"a", "b"}, tt.fileLines, "ask\n", response)
			if got != tt.want {
				t.Errorf("got\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}
