package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"

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

		req := request{
			SessionName: "",
			Type:        "create",
			Line:        strings.Join(args, " "),
		}

		j, err := json.Marshal(req)
		if err != nil {
			fmt.Println("json serialize error:", err)
			return
		}

		if err := conn.WriteMessage(websocket.TextMessage, j); err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}

		finishCh := make(chan bool)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-finishCh:
					return
				default:
					//
				}
				t, out, err := conn.ReadMessage()
				if err != nil {
					break
				}
				if len(out) == 0 {
					continue
				}
				fmt.Println(fmt.Sprintf("type: %v, out: %v", t, string(out)))
			}
		}()

		go func() {
			defer func() {
				wg.Done()
				finishCh <- true
			}()
			reader := bufio.NewReader(os.Stdin)
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					fmt.Println("stdin read error(main):", err)
					return
				}
				if err := conn.WriteMessage(websocket.TextMessage, []byte(line)); err != nil {
					fmt.Println("server websocket write error:", err)
					fmt.Println("error str:", []byte(line))
					return
				}
			}
		}()

		wg.Wait()
	}
}
