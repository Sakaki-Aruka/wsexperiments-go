package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
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

type requestType int

const (
	Create requestType = iota
	Connect
)

type request struct {
	SessionName string      `json:"session_name"`
	Type        requestType `json:"type"`
	Line        []byte      `json:"line"`
}

type createRequest struct {
	Env     []string `json:"env"`
	Pwd     string   `json:"pwd"`
	Command []string `json:"command"`
}

type session struct {
	cmd       *exec.Cmd
	out       chan []byte
	err       chan []byte
	running   bool
	wg        *sync.WaitGroup
	name      string
	connected []*websocket.Conn
}

var sessions = make(map[string]*session)

func handler(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Println("error: " + err.Error())
		return
	}
	defer conn.Close()

	for {
		t, d, err := conn.ReadMessage()
		if err != nil {
			fmt.Println("websocket read message error:", err)
			return
		}

		if t == websocket.TextMessage {
			var req request
			if err := json.Unmarshal(d, &req); err != nil {
				log.Println("failed to parse request json.", err)
				continue
			}

			if req.Type == Create {
				var cReq createRequest
				if err := json.Unmarshal(req.Line, &cReq); err != nil {
					log.Println("failed to parse createRequest json.", err)
					continue
				}

				if len(cReq.Command) == 0 {
					log.Println("cannot run empty command")
					continue
				}

				cmd := exec.Command(cReq.Command[0], cReq.Command[1:]...)
				cmd.Env = cReq.Env
				stdout, err := cmd.StdoutPipe()
				if err != nil {
					fmt.Println("failed to get stdout pipe")
					return
				}
				stdoutScanner := bufio.NewReader(stdout)
				outChannel := make(chan []byte, 4096)
				sig := make(chan os.Signal)
				signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT, syscall.SIGKILL, os.Interrupt)
				done := make(chan struct{})

				s := session{
					cmd:       cmd,
					out:       make(chan []byte, 4096),
					err:       make(chan []byte, 4096),
					running:   true,
					wg:        &sync.WaitGroup{},
					name:      req.SessionName,
					connected: []*websocket.Conn{conn},
				}

				sessions[s.name] = &s
				s.wg.Add(3)

				go func() {
					defer func() {
						close(outChannel)
						s.wg.Done()
					}()
					buf := make([]byte, 4096)
					for {
						n, err := stdoutScanner.Read(buf)
						if n > 0 {
							outChannel <- buf[:n]
						}

						if err != nil {
							fmt.Println("read error:", err)
							break
						}
					}
					done <- struct{}{}
				}()

				go func() {
					defer func() {
						s.wg.Done()
						// TODO: delete connection from s.connected
					}()
					for {
						select {
						case <-done:
							close(outChannel)
							close(done)
							return

						case d, ok := <-outChannel:
							if !ok {
								return
							}
							if len(s.connected) > 0 {
								for _, c := range s.connected {
									if err := c.WriteMessage(websocket.TextMessage, d); err != nil {
										fmt.Println("write message error:", err)
										return
									}
								}
							} else {
								io.Discard.Write(d)
							}
						}
					}
				}()

				go func() {
					defer s.wg.Done()
					select {
					case s := <-sig:
						if err := cmd.Process.Signal(s); err != nil {
							fmt.Println("signal error:", err)
						}
					case <-done:
						return
					}
				}()

				if err := cmd.Start(); err != nil {
					fmt.Println("process start error:", err)
					return
				}

				s.wg.Wait()

			} else if req.Type == Connect {
				//
			}
		}
	}
}

func Run() {
	http.HandleFunc("/socket", handler)
}
