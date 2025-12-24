package main

import (
	"cmp"
	_ "embed"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

//go:embed chat.html
var chatHTML []byte

func upHandler(w http.ResponseWriter, r *http.Request) {
	slog.Info("Health request", "method", r.Method, "url", r.URL)
	w.WriteHeader(http.StatusOK)
}

func helloHandler(host string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host = cmp.Or(r.Header.Get("X-Kamal-Target"), host)

		w.Header().Add("Content-Type", "text/html")
		fmt.Fprintf(w, "<p>Hello from <strong>%s</strong> at <strong>%s</strong></p>\n",
			host,
			time.Now().Format(time.RFC3339),
		)

		script := `
			<p>Server sent events list:</p>

			<script>
				const evtSource = new EventSource("/sse");

				const eventList = document.createElement("ul");
				document.body.appendChild(eventList);

				evtSource.onmessage = (event) => {
					const newElement = document.createElement("li");
					newElement.textContent = "message: " + event.data;
					eventList.appendChild(newElement);
				};
			</script>
		`
		fmt.Fprintf(w, script)

		slog.Info("Request", "host", host, "request_id", r.Header.Get("X-Request-ID"), "method", r.Method, "url", r.URL)
	}
}

func sseHandler(w http.ResponseWriter, r *http.Request) {
	// Set headers to mimic SSE
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	w.Write([]byte("data: hello\n\n"))

	rc := http.NewResponseController(w)
	if err := rc.Flush(); err != nil {
		fmt.Println("Couldn't flush!")
		fmt.Println(err)
	}

	time.Sleep(4 * time.Second)
	w.Write([]byte("data: world!!!\n\n"))
	if err := rc.Flush(); err != nil {
		fmt.Println("Couldn't flush!")
		fmt.Println(err)
	}

	time.Sleep(40 * time.Second)
	w.Write([]byte("data: the end!!!\n\n"))
}

func serveChat(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	slog.Info("Serve /chat request", "method", r.Method, "url", r.URL)
	w.Write(chatHTML)
}

func main() {
	host, err := os.Hostname()
	if err != nil {
		panic(err)
	}

	hub := newHub()
	go hub.run()

	http.HandleFunc("/", serveChat)
	http.HandleFunc("/up", upHandler)
	http.HandleFunc("/test-sse", helloHandler(host))
	http.HandleFunc("/sse", sseHandler)
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		serveWs(hub, w, r)
	})
	panic(http.ListenAndServe(":80", nil))
}
