package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
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

func normal(w http.ResponseWriter, _ *http.Request) {
	_, err := w.Write([]byte("hello world\n"))
	if err != nil {
		fmt.Println("failed to write response:", err)
	}
	w.WriteHeader(http.StatusOK)
}

type cReq struct {
	SessionName string   `json:"session_name"`
	Env         []string `json:"env"`
	Pwd         string   `json:"pwd"`
	Command     string   `json:"command"`
}

func register(w http.ResponseWriter, r *http.Request) {
	responseCode := 200
	defer func() {
		w.WriteHeader(responseCode)
		if err := r.Body.Close(); err != nil {
			log.Println("request body close error:", err)
		}
	}()

	if r.Method != http.MethodPost {
		log.Println(fmt.Sprintf("method '%v' is not allowed. (source: %v)", r.Method, r.RemoteAddr))
		responseCode = http.StatusBadRequest
		return
	}

	buf := new(bytes.Buffer)
	io.Copy(buf, r.Body)
	body := buf.Bytes()

	var c cReq
	if err := json.Unmarshal(body, &c); err != nil {
		log.Fatal("create request unmarshal error:", err)
		return
	}

	res, err := json.Marshal(c)
	if err != nil {
		log.Fatal("json marshal error:", err)
		return
	}
	if _, err := w.Write(res); err != nil {
		log.Fatal("response write error:", err)
		return
	}

	if err := startProcess(c); err != nil {
		log.Println("process start error:", err)
		return
	}

}

func startProcess(c cReq) error {
	commands := strings.Split(c.Command, " ")
	cmd := exec.Command(commands[0], commands[1:]...)
	cmd.Env = c.Env
	cmd.Dir = c.Pwd
	buf := make([]byte, 4096)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		log.Println("stdout get error:", err)
		return err
	}
	go func() {
		u := url.URL{Scheme: "ws", Host: "localhost:8888", Path: "/socket"}
		c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)

		defer func() {
			if cmd.ProcessState != nil && !cmd.ProcessState.Exited() {
				cmd.Process.Kill()
			}
			cmd.Wait()
			c.Close()
		}()

		if err != nil {
			log.Println("dial:", err)
			return
		}

		for {
			n, err := stdout.Read(buf)
			if err != nil {
				log.Println("stdout error:", err)
				return
			}

			if n > 0 {
				data := buf[:n]
				c.WriteMessage(1, data)
			}
		}
	}()

	if err := cmd.Start(); err != nil {
		log.Println("process start error:", err)
		return err
	}

	return nil
}

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		// run server
		http.HandleFunc("/socket", p)
		http.HandleFunc("/http", normal)
		http.HandleFunc("/register", register)
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
