package translate

import (
	"strings"
	"testing"
)

func TestOpenAIStreamOnlyAcceptsCompletedOutputText(t *testing.T) {
	body := `data: {"type":"response.reasoning_summary_text.delta","delta":"reasoning, not a translation"}
data: {"type":"response.output_text.delta","delta":"Bon"}
data: {"type":"response.function_call_arguments.delta","delta":"tool arguments"}
data: {"type":"response.output_text.delta","delta":"jour"}
data: {"type":"response.completed","response":{"status":"completed"}}
`
	text, err := extractStreamingResponseText([]byte(body))
	if err != nil || text != "Bonjour" {
		t.Fatalf("stream text = %q, %v", text, err)
	}
}

func TestOpenAIStreamDoesNotPublishPartialTranslation(t *testing.T) {
	for _, ending := range []string{
		"", "data: [DONE]\n",
		"data: {malformed}\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}",
		`data: {"type":"response.failed"}`,
		`data: {"type":"response.incomplete"}`,
		`data: {"type":"error","message":"failed"}`,
		`data: {"type":"response.completed","response":{"status":"incomplete"}}`,
		`data: {"type":"response.completed","response":null}`,
		`data: {"type":"response.completed","response":{}}`,
		`data: {"type":"response.completed"}`,
	} {
		body := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n" + ending
		text, err := extractStreamingResponseText([]byte(body))
		if err == nil || text != "" {
			t.Fatalf("published partial output for %q: %q, %v", ending, text, err)
		}
	}
}

func TestOpenAICompletedResponseWithoutDeltas(t *testing.T) {
	body := `data: {"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"Bonjour"}]}]}}`
	text, err := extractStreamingResponseText([]byte(body))
	if err != nil || !strings.EqualFold(text, "Bonjour") {
		t.Fatalf("completed output = %q, %v", text, err)
	}
}
