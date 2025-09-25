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

var u = websocket.Upgrader{}

func main() {
	args := flag.Args()
	if len(args) == 0 {
		server()
	} else {
		client(args)
	}
}

func client(args []string) {
	Url := url.URL{Scheme: "ws", Host: ":8888", Path: "/socket"}
	conn, _, err := websocket.DefaultDialer.Dial(Url.String(), nil)
	if err != nil {
		fmt.Println("client connect error: " + err.Error())
		return
	}

	if err := conn.WriteMessage(1, []byte(strings.Join(args, " "))); err != nil {
		fmt.Println("websocket write error: " + err.Error())
		return
	}
	t, d, err := conn.ReadMessage()
	if err != nil {
		fmt.Println("websocket response error: " + err.Error())
		return
	}

	fmt.Println(fmt.Sprintf("type: %v, content (Received): %v", t, string(d)))
	conn.Close()
}

func h(w http.ResponseWriter, r *http.Request) {
	conn, err := u.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("conn upgrade error: " + err.Error())
		return
	}
	defer conn.Close()

	for {
		t, d, err := conn.ReadMessage()
		if err != nil {
			fmt.Println("websocket read message error: " + err.Error())
			return
		}

		str := string(d)
		fmt.Println(fmt.Sprintf("type: %v, content: %v", t, str))
		if err := conn.WriteMessage(websocket.TextMessage, d); err != nil {
			fmt.Println(fmt.Sprintf("write error: " + err.Error()))
		}
	}
}

func server() {
	if _, err := os.Stat("test.lock"); err == nil {
		return
	}
	if err := os.WriteFile("test.lock", make([]byte, 0), 0770); err != nil {
		fmt.Println("lock file creation error: " + err.Error())
	}

	http.HandleFunc("/socket", h)
	if err := http.ListenAndServe(":8888", nil); err != nil {
		fmt.Println("http server error: " + err.Error())
		return
	}
}
