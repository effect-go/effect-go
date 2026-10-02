// feedserver serves an RSS feed: the first request at once, later ones
// after 60 seconds, unless the client gives up first.
package main

import (
	"fmt"
	"net/http"
	"sync/atomic"
	"time"
)

const feed = `<?xml version="1.0"?><rss version="2.0"><channel><title>slow</title><link>http://127.0.0.1:18090/</link><item><title>one</title><link>http://127.0.0.1:18090/1</link><guid>1</guid></item></channel></rss>`

func main() {
	var n atomic.Int32
	http.HandleFunc("/feed.xml", func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) > 1 {
			fmt.Println("slow request started")
			select {
			case <-time.After(60 * time.Second):
			case <-r.Context().Done():
				fmt.Println("client gave up")
				return
			}
		}
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, feed)
	})
	http.ListenAndServe("127.0.0.1:18090", nil)
}
