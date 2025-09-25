package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	HandshakeTimeout:  0,
	ReadBufferSize:    1024,
	WriteBufferSize:   1024,
	WriteBufferPool:   nil,
	Subprotocols:      nil,
	Error:             nil,
	CheckOrigin:       nil,
	EnableCompression: false,
}

type session struct {
	connections map[*websocket.Conn]bool
	cmd         *exec.Cmd
}

var sessions = make(map[*websocket.Conn]session)

func handler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("error: " + err.Error())
		return
	}

	t, data, err := conn.ReadMessage()
	if err != nil {
		return
	}

	if t != websocket.TextMessage {
		return
	}

	cmdStr := string(data)
	args := strings.Fields(cmdStr)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = os.Environ()
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return
	}

	if err := cmd.Start(); err != nil {
		return
	}

	sessions[conn] = session{
		cmd:         cmd,
		connections: map[*websocket.Conn]bool{conn: true},
	}

	fmt.Println(cmd.ProcessState.ExitCode())

	outCh := make(chan string)

	go func() {
		cmd.Wait()
		close(outCh)
	}()

	go func() {
		for _, ok := <-outCh; ok; {
			time.Sleep(time.Millisecond * 500)
		}
		fmt.Println("outCh closed")
	}()

	go func() {
		// Write contents to a channel
		defer func() {
			if err := stdoutPipe.Close(); err != nil {
				fmt.Println(err.Error())
			}
		}()

		for cmd.ProcessState.ExitCode() == -1 { //!cmd.ProcessState.Exited() {
			var o []byte
			if _, err := stdoutPipe.Read(o); err != nil {
				continue
			}

			outCh <- string(o)
		}
	}()

	go func() {
		// Read contents from a channel
		defer func() {
			if err := conn.Close(); err != nil {
				fmt.Println(err.Error())
			}
			delete(sessions, conn)
		}()
		for o, ok := <-outCh; ok; {
			if s, exists := sessions[conn]; exists {
				for c := range s.connections {
					if err := c.WriteMessage(websocket.TextMessage, []byte(o)); err != nil {
						//fmt.Println(err.Error())
					}
				}
			} else {
				if _, err := io.Discard.Write([]byte(o)); err != nil {
					fmt.Println(err.Error())
					continue
				}
			}
		}
	}()
}

func Run() {
	http.HandleFunc("/socket", handler)
}
