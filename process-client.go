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

			var dp dataPost
			if err := json.Unmarshal(message, &dp); err != nil {
				log.Println("post unmarshal error:", err)
				continue
				//return
			}

			if dp.Type == "process" && len(connectionMap[dp.SessionName]) != 0 {
				for connected := range connectionMap[dp.SessionName] {
					if err := connected.WriteMessage(1, dp.Line); err != nil {
						//debug
						log.Println("name delete from map-2:", dp.SessionName)
						delete(connectionMap, dp.SessionName)
						log.Println("server broadcast error:", err)
						connected.Close()
					}
				}
			}
			log.Print("[out]", string(dp.Line))
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

type dataPost struct {
	Type        string `json:"type"`
	SessionName string `json:"session_name"`
	Line        []byte `json:"line"`
}

var connectionMap = make(map[string]map[*websocket.Conn]bool)

// Key: SessionName, Value: Connections

func connect(w http.ResponseWriter, r *http.Request) {
	// header parse here?
	// websocket.DefaultDialer.Dial 's second argument can receive http.Header <<-- USE THIS!!!
	// 'X-WS-SESSION-NAME'

	sessionName := r.Header.Get("X-WS-SESSION-NAME")
	if _, exists := connectionMap[sessionName]; !exists {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(fmt.Sprintf("'%v' not exists session name", sessionName)))
		return
	}

	conn, _ := U.Upgrade(w, r, nil)
	connectionMap[sessionName][conn] = true

	//debug
	log.Println("connection map:", connectionMap)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer func() {
			wg.Done()

			//debug
			log.Println("name delete from map-3:", sessionName)
			delete(connectionMap[sessionName], conn)
		}()
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Println("websocket err:", err)
				return
			}

			var dp dataPost
			if err := json.Unmarshal(message, &dp); err != nil {
				log.Println("post unmarshal error:", err)
				continue
			}

			if dp.Type == "process" {
				log.Print("[client]: " + string(dp.Line))
			}
		}
	}()
	wg.Wait()
}

func register(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if err := r.Body.Close(); err != nil {
			log.Println("request body close error:", err)
		}
	}()

	if r.Method != http.MethodPost {
		log.Println(fmt.Sprintf("method '%v' is not allowed. (source: %v)", r.Method, r.RemoteAddr))
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
		conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)

		defer func() {
			if cmd.ProcessState != nil && !cmd.ProcessState.Exited() {
				cmd.Process.Kill()
			}
			cmd.Wait()
			conn.Close()

			//debug
			log.Println("name delete from map-1:", c.SessionName)
			for connected := range connectionMap[c.SessionName] {
				connected.Close()
			}
			delete(connectionMap, c.SessionName)
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
				dataPost := dataPost{
					Type:        "process",
					SessionName: c.SessionName,
					Line:        data,
				}
				j, _ := json.Marshal(dataPost)
				conn.WriteMessage(1, j)
			}
		}
	}()

	if err := cmd.Start(); err != nil {
		log.Println("process start error:", err)
		return err
	}

	connectionMap[c.SessionName] = map[*websocket.Conn]bool{}

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
		http.HandleFunc("/connect", connect)
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
