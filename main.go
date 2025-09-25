package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/gorilla/websocket"
)

func main() {
	flag.Parse()
	if len(flag.Args()) == 0 {
		// Run daemon
		Run()
		if err := http.ListenAndServe("localhost:8888", nil); err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		return
	} else {
		args := flag.Args()
		u := url.URL{Scheme: "ws", Host: "localhost:8888", Path: "/socket"}
		conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		defer conn.Close()

		if err := conn.WriteMessage(websocket.TextMessage, []byte(strings.Join(args, " "))); err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}

		finishCh := make(chan bool)
		go func() {
			for {
				t, out, err := conn.ReadMessage()
				if err != nil {
					break
				}
				if len(out) == 0 {
					continue
				}
				fmt.Println(fmt.Sprintf("type: %v, out: %v", t, string(out)))
			}
			close(finishCh)
		}()
		<-finishCh
	}
}
