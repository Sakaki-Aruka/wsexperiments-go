package main

import (
	"flag"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"

	"github.com/gorilla/websocket"
)

var U = websocket.Upgrader{}

func p(w http.ResponseWriter, r *http.Request) {
	conn, _ := U.Upgrade(w, r, nil)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Println("websocket err:", err)
				return
			}
			log.Print("[out]", string(message))
		}
	}()
	wg.Wait()
}

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		// run server
		http.HandleFunc("/socket", p)
		if err := http.ListenAndServe(":8888", nil); err != nil {
			log.Println("http server error:", err)
			return
		}
	} else {
		// <command...>
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Env = os.Environ()

		u := url.URL{Scheme: "ws", Host: "localhost:8888", Path: "/socket"}

		c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
		if err != nil {
			log.Fatal("dial:", err)
			return
		}

		defer func() {
			c.Close()
		}()

		sig := make(chan os.Signal)
		signal.Notify(sig, os.Interrupt, syscall.SIGINT, syscall.SIGKILL)

		end := make(chan struct{})

		var wg sync.WaitGroup
		wg.Add(1)

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Println("stdout get error")
			return
		}

		buffer := make([]byte, 4096)

		go func() {
			defer func() {
				wg.Done()
				close(sig)
				close(end)
			}()
			for {
				select {
				case <-sig:
					return
				case <-end:
					return
				default:
					n, err := stdout.Read(buffer)
					if err != nil {
						log.Println("stdout error:", err)
						return
					}

					if n > 0 {
						data := buffer[:n]
						//log.Print("[out]", string(data))
						c.WriteMessage(1, data)
					}
				}
			}
		}()

		if err := cmd.Start(); err != nil {
			log.Println("start error:", err)
			end <- struct{}{}
		}

		wg.Wait()
	}
}
