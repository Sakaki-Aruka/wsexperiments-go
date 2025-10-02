package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"syscall"

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
		// /<executable file> <session name> <commands...>
		args := flag.Args()
		if len(args) > 1 {
			u := url.URL{Scheme: "ws", Host: "localhost:8888", Path: "/socket"}
			conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
			if err != nil {
				fmt.Println(err.Error())
				os.Exit(1)
			}
			defer conn.Close()
			currentDir, _ := os.Getwd()
			cr := createRequest{
				Env:     os.Environ(),
				Pwd:     currentDir,
				Command: args[1:],
			}

			j, _ := json.Marshal(cr)

			r := request{
				SessionName: args[0],
				Type:        Create,
				Line:        j,
			}

			in := bufio.NewWriter(os.Stdin)

			done := make(chan struct{})
			sig := make(chan os.Signal, 1)
			signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGTERM, syscall.SIGKILL)
			var wg sync.WaitGroup
			wg.Add(2)

			go func() {
				// print received messages
				defer func() {
					wg.Done()
				}()

				for {
					select {
					case <-sig:
						return
					case <-done:
						return

					default:
						t, d, err := conn.ReadMessage()
						if err != nil {
							log.Println("read error:", err)
							done <- struct{}{}
							return
						}

						if t == websocket.TextMessage {
							fmt.Print(string(d))
						} else if t >= websocket.CloseNormalClosure {
							// close
							log.Println("websocket connection closed by a server")
							conn.Close()
							done <- struct{}{}
							return
						}
					}
				}

			}()

			go func() {
				// read client input and send to a server
				defer func() {
					conn.WriteMessage(websocket.CloseNormalClosure, make([]byte, 0))
					wg.Done()
				}()

				buffer := make([]byte, 4096)
				for {
					select {
					case <-sig:
						return
					case <-done:
						return

					default:
						n, err := in.Write(buffer)
						if n > 0 {
							if err := conn.WriteMessage(websocket.TextMessage, buffer[:n]); err != nil {
								// websocket write error
								log.Println("websocket write error:", err)
								return
							}

							if err != nil {
								// stdin write error
								log.Println("stdin write error:", err)
								return
							}
						}
					}
				}
			}()

			if err := conn.WriteJSON(r); err != nil {
				log.Println("failed to init message:", err)
				return
			}

			wg.Wait()
		}
	}
}
