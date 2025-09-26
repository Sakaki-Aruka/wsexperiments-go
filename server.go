package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"

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
	in          chan string
	cmd         *exec.Cmd
}

type request struct {
	SessionName string `json:"session_name"`
	Type        string `json:"type"` // [create | connect | ]
	Line        string `json:"line"`
}

var sessions = make(map[*websocket.Conn]*session)

func handler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("error: " + err.Error())
		return
	}
	defer conn.Close()

	_, d, err := conn.ReadMessage()
	var request request
	if err := json.Unmarshal(d, &request); err != nil {
		fmt.Println("json parse error:", err)
		return
	}

	s, exists := sessions[conn]
	if !exists && request.Type == "create" {
		var wg sync.WaitGroup
		_, err := getCmd(strings.Split(request.Line, " "), conn, &wg)
		if err != nil {
			fmt.Println("get process error:", err)
			return
		}
		wg.Wait()
	} else if exists && request.Type == "connect" {
		s.connections[conn] = true
		go func() {
			for {
				_, d, err := conn.ReadMessage()
				if err != nil {
					//
				}
				s.in <- string(d)
			}
		}()
	}
}

func getCmd(args []string, conn *websocket.Conn, wg *sync.WaitGroup) (session, error) {
	if len(args) == 0 {
		return session{}, fmt.Errorf("need argsments to start a process")
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = os.Environ()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Println("process stdout pipe get error:", err)
		return session{}, err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		fmt.Println("process stdin pipe get error:", err)
		return session{}, err
	}
	stdoutReader := bufio.NewReader(stdout)

	done := make(chan struct{}, 1)
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGKILL, os.Interrupt)

	s := session{
		connections: map[*websocket.Conn]bool{conn: true},
		in:          make(chan string), //, 1024),
		cmd:         cmd,
	}

	wg.Add(1)

	go func() {
		// server, receive msg -> process
		defer func() {
			done <- struct{}{}
		}()
		for {
			select {
			case <-sig:
				return

			case <-done:
				return

			default:
			}
			in, ok := <-s.in
			if !ok {
				fmt.Println("stdin channel closed")
				return
			}
			if _, err := stdin.Write([]byte(in)); err != nil {
				fmt.Println("process read []byte error:", err)
				return
			}
		}
	}()

	go func() {
		select {
		case si := <-sig:
			delete(sessions, conn)
			fmt.Println("websocket server stop signal received:", si.String())
			wg.Done()
			//os.Exit(10)
			return
		case <-done:
			delete(sessions, conn)
			fmt.Println("websocket server finished with error")
			conn.Close()
			wg.Done()
			//os.Exit(20)
			return
		}
	}()

	go func() {
		// server, process stdout -> client
		defer func() {
			done <- struct{}{}
			close(s.in)
		}()
		for {
			select {
			case <-sig:
				return

			case <-done:
				return

			default:
				//
			}
			line, err := stdoutReader.ReadString('\n')
			if err != nil {
				fmt.Println("stdin read error:", err)
				return
			}

			req := request{
				SessionName: "",
				Type:        "",
				Line:        line,
			}

			j, _ := json.Marshal(req)

			for c := range s.connections {
				if err := c.WriteMessage(websocket.TextMessage, j); err != nil {
					fmt.Println("websocket server write error:", err)
					fmt.Println("write error content:", string(j))
					return
				}
			}
		}
	}()

	if err := cmd.Start(); err != nil {
		fmt.Println("process start error:", err)
		return session{}, err
	}

	return s, nil
}

func Run() {
	http.HandleFunc("/socket", handler)
}
