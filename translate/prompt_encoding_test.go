package translate

import (
	"encoding/json"
	"testing"
)

func TestPromptSourceEncodingPreservesDocumentationLiterals(t *testing.T) {
	for _, source := range []string{
		`${BUILD_DIR}/usr/share/man`,
		`\${BUILD_DIR}/bin`,
		`B<${BUILD_DIR}> and \f[B]$HOME\fP`,
		"literal \\n versus newline\n and tab\t",
		"cp \"${BUILD_DIR}/file\" \\\n  /destination",
		`{{- value}} $t(key) %1$s <xliff:g id="path">${BUILD_DIR}</xliff:g>`,
	} {
		t.Run(source, func(t *testing.T) {
			var decoded string
			encoded := escapeForPrompt(source)
			if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
				t.Fatalf("source is not unambiguous JSON: %q: %v", encoded, err)
			}
			if decoded != source {
				t.Fatalf("source changed: got %q, want %q", decoded, source)
			}
		})
	}
	if escapeForPrompt(`\n`) == escapeForPrompt("\n") {
		t.Fatal("literal backslash-n and newline have identical prompt representations")
	}
}
