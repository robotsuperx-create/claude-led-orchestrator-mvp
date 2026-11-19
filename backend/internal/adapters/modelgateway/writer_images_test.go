package modelgateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A message with pictures is sent as text and image parts; one without is
// sent exactly as before.
func TestAMessageWithPicturesIsSentAsParts(t *testing.T) {
	plain, err := json.Marshal(ChatMessage{Role: "user", Content: "hi"})
	if err != nil || string(plain) != `{"role":"user","content":"hi"}` {
		t.Fatalf("plain message = %s, %v", plain, err)
	}
	withImage, err := json.Marshal(ChatMessage{Role: "user", Content: "look", Images: [][]byte{[]byte("jpeg")}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"role":"user","content":[{"type":"text","text":"look"},{"type":"image_url","image_url":{"url":"data:image/jpeg;base64,` + base64.StdEncoding.EncodeToString([]byte("jpeg")) + `"}}]}`
	if string(withImage) != want {
		t.Fatalf("message = %s\nwant      %s", withImage, want)
	}
	var back ChatMessage
	if err := json.Unmarshal([]byte(`{"role":"assistant","content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}`), &back); err != nil || back.Content != "ab" {
		t.Fatalf("parts read back as %q, %v", back.Content, err)
	}
}

// The writer is shown the screenshots it is given, desktop first, each part
// named, and told which is which.
func TestAWriterSeesTheScreenshotsItIsGiven(t *testing.T) {
	server, body := chatServer(t, `{"summary":"s","edits":[]}`)
	looks := []ports.VisualShot{
		{Page: "index.html", Viewport: "phone", Tiles: [][]byte{[]byte("p1"), []byte("p2")}, Findings: []string{"The page scrolls sideways."}},
		{Page: "index.html", Viewport: "desktop", JPEG: []byte("d1")},
	}
	if _, err := claudeAdapterFor(t, server.URL).ProposeEdits(context.Background(), ports.CodeAuthorRequest{Instructions: "polish", Looks: looks}); err != nil {
		t.Fatal(err)
	}
	if strings.Count(*body, `"type":"image_url"`) != 3 {
		t.Fatalf("request = %s, want three pictures", *body)
	}
	first := strings.Index(*body, base64.StdEncoding.EncodeToString([]byte("d1")))
	second := strings.Index(*body, base64.StdEncoding.EncodeToString([]byte("p1")))
	if first < 0 || second < 0 || first > second {
		t.Fatalf("desktop is not shown first: %s", *body)
	}
	for _, want := range []string{`\"screenshots\":[{\"image\":1,\"page\":\"index.html\",\"viewport\":\"desktop\"}`, `\"part\":\"1 of 2, top to bottom\"`, `The page scrolls sideways.`} {
		if !strings.Contains(*body, want) {
			t.Fatalf("request lacks %s: %s", want, *body)
		}
	}
	images, _ := writerImages([]ports.VisualShot{{Viewport: "desktop", Tiles: make([][]byte, 30)}})
	if len(images) != 0 {
		t.Fatalf("empty pieces were sent: %d", len(images))
	}
	many := make([][]byte, 30)
	for i := range many {
		many[i] = []byte{byte(i + 1)}
	}
	if images, _ := writerImages([]ports.VisualShot{{Viewport: "desktop", Tiles: many}}); len(images) != maxWriterImages {
		t.Fatalf("%d pictures, want at most %d", len(images), maxWriterImages)
	}
}
