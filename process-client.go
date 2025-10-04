package main

import (
	"bufio"
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
	"time"

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

			if dp.Type == "process" {
				for connected := range connectionMap[dp.SessionName] {
					if err := connected.WriteMessage(1, message); err != nil {
						//debug
						log.Println("name delete from map-2:", dp.SessionName)
						delete(connectionMap, dp.SessionName)
						log.Println("server broadcast error:", err)
						connected.Close()
					}
				}
				log.Print("[out]", string(dp.Line))
			} else if dp.Type == "user" {
				log.Println(fmt.Sprintf("[p / client]: %v", string(message)))
				conn.WriteMessage(1, message)
			}
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
var processes = make(map[string]*websocket.Conn)

// Key: SessionName, Value: Connections

func preconnect(w http.ResponseWriter, r *http.Request) {
	sessionName := r.Header.Get("X-WS-SESSION-NAME")
	if len(sessionName) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte("invalid session name."))
		return
	}

	conn, _ := U.Upgrade(w, r, nil)
	defer conn.Close()
	for {
		if _, exists := connectionMap[sessionName]; !exists {
			time.Sleep(10 * time.Millisecond)
			continue
		} else {
			conn.Close()
			return
		}
	}

}

func connect(w http.ResponseWriter, r *http.Request) {
	// header parse here?
	// websocket.DefaultDialer.Dial 's second argument can receive http.Header <<-- USE THIS!!!
	// 'X-WS-SESSION-NAME'

	sessionName := r.Header.Get("X-WS-SESSION-NAME")
	if _, exists := connectionMap[sessionName]; !exists {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(fmt.Sprintf("'%v' not exists session name", sessionName)))

		//debug
		log.Println(fmt.Sprintf("not exists map: %v", connectionMap))
		log.Println("nanosecond:", time.Now())

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

			if dp.Type == "user" {
				log.Print("[connect / client]: " + string(dp.Line))
				if targetConn, exists := processes[dp.SessionName]; exists {
					if err := targetConn.WriteMessage(websocket.TextMessage, message); err != nil {
						log.Println("user input write error:", err)
						continue
					}

					//debug
					log.Println("process write ok:", string(message))
					log.Println(fmt.Sprintf("processes: %v", processes))
				}
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

	stdin, err := cmd.StdinPipe()
	if err != nil {
		log.Println("stdin get error:", err)
		return err
	}

	end := make(chan struct{})

	u := url.URL{Scheme: "ws", Host: "localhost:8888", Path: "/socket"}
	header := http.Header{}
	header.Set("X-WS-SESSION-NAME", c.SessionName)
	connectionMap[c.SessionName] = make(map[*websocket.Conn]bool)
	conn, _, err := websocket.DefaultDialer.Dial(u.String(), header)

	go func() {
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
			delete(processes, c.SessionName)
			//close(end)
		}()

		if err != nil {
			log.Println("dial:", err)
			return
		}

		processes[c.SessionName] = conn

		for {
			select {
			case <-end:
				return
			default:
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
		}
	}()

	go func() {
		defer func() {
			end <- struct{}{}
		}()
		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Println("process received message error:", err)
				return
			}

			//debug
			log.Println("process received message:", string(message))

			var userPost dataPost
			if err := json.Unmarshal(message, &userPost); err != nil {
				log.Println("process received message unmarshal error:", err)
				return
			}

			if userPost.Type == "user" {
				in := userPost.Line
				if !strings.HasSuffix(string(in), "\n") {
					in = append(in, "\n"...)
				}
				log.Print(fmt.Sprintf("in: %v", string(in)))

				n, err := stdin.Write(in)
				if err != nil {
					log.Println("process write message error:", err)
					return
				}

				//debug
				log.Println(fmt.Sprintf("wrote %v bytes to stdin.", n))
			}
		}
	}()

	if err := cmd.Start(); err != nil {
		log.Println("process start error:", err)
		delete(connectionMap, c.SessionName)
		return err
	}

	//debug
	log.Println(fmt.Sprintf("added map: %v", connectionMap))
	log.Println("nanosecond:", time.Now())

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
		http.HandleFunc("/preconnect", preconnect)
		if err := http.ListenAndServe(":8888", nil); err != nil {
			log.Println("http server error:", err)
			return
		}
	} else {
		// <sessionName> <command...>

		// === process register === //
		u := url.URL{Scheme: "http", Host: "localhost:8888", Path: "/register"}
		pwd, _ := os.Getwd()
		creq := cReq{
			SessionName: args[0],
			Env:         os.Environ(),
			Pwd:         pwd,
			Command:     strings.Join(args[1:], " "),
		}

		//debug
		fmt.Println("command:", strings.Join(args[1:], " "))

		j, _ := json.Marshal(creq)
		res, err := http.Post(u.String(), "application/json", bytes.NewReader(j))
		if err != nil {
			log.Println("http post error:", err)
			return
		}

		//defer res.Body.Close()
		res.Body.Close()

		if res.StatusCode != http.StatusOK {
			log.Println("http post response status error:", res.StatusCode)
			body := make([]byte, res.ContentLength)
			res.Body.Read(body)
			log.Println("response body:", string(body))
		}

		// --- preconnect --- //
		preUrl := url.URL{Scheme: "ws", Host: "localhost:8888", Path: "/preconnect"}
		preHeader := http.Header{}
		preHeader.Set("X-WS-SESSION-NAME", args[0])
		preConn, _, err := websocket.DefaultDialer.Dial(preUrl.String(), preHeader)
		if err != nil {
			log.Println("preconnect dial error:", err)
		}

		for {
			_, _, err := preConn.ReadMessage()
			if err != nil {
				log.Println("preconnect closed with error:", err)
				break
			}
		}

		// --- connect to a websocket --- //
		dp := dataPost{
			Type:        "user",
			SessionName: args[0],
			Line:        []byte(strings.Join(args[1:], " ")),
		}
		wsu := url.URL{Scheme: "ws", Host: "localhost:8888", Path: "/connect"}
		header := http.Header{}
		header.Add("X-WS-SESSION-NAME", args[0])
		conn, _, err := websocket.DefaultDialer.Dial(wsu.String(), header)
		if err != nil {
			log.Println("websocket dial error:", err)
			log.Println("url:", wsu.String())
			return
		}

		defer func() {
			conn.Close()
		}()

		sig := make(chan os.Signal, 3)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM, syscall.SIGKILL)

		end := make(chan struct{}, 3)
		scan := make(chan bool, 1)

		connClosed := false
		sentEnd := false

		stdin := bufio.NewScanner(os.Stdin)
		var wg sync.WaitGroup
		wg.Add(3)

		go func() {
			defer func() {
				if !sentEnd {
					end <- struct{}{}
					sentEnd = true

					//debug
					log.Println("send end 1")
				}
				if !connClosed {
					if err := conn.Close(); err != nil {
						log.Println("websocket connection close error:", err)
					}
					connClosed = true
				}
				wg.Done()

				//debug
				log.Println("wg done 1")
			}()
			select {
			case <-sig:
				return
			case <-end:
				return

			}
		}()

		go func() {
			// display process stdout
			defer func() {
				if !sentEnd {
					end <- struct{}{}
					sentEnd = true

					//debug
					log.Println("send end 2")
				}
				wg.Done()

				//debug
				log.Println("wg done 2")
			}()
			for {
				_, message, err := conn.ReadMessage()
				if err != nil {
					log.Println("websocket read error:", err)
					return
				}
				var dp dataPost
				if err := json.Unmarshal(message, &dp); err != nil {
					log.Println("received data unmarshal error:", err)
					return
				}

				if dp.Type == "process" {
					fmt.Print(string(dp.Line))
				}
			}
		}()

		go func() {
			defer func() {
				if !sentEnd {
					end <- struct{}{}

					//debug
					log.Println("send end 3")
				}
				wg.Done()

				//debug
				log.Println("wg done 3")
			}()
			for {
				select {
				case <-sig:
					return
				case <-end:
					return
				case scan <- stdin.Scan():
					if <-scan {
						input := stdin.Bytes()
						userPost := dataPost{
							Type:        "user",
							SessionName: dp.SessionName,
							Line:        input,
						}
						userJ, _ := json.Marshal(userPost)
						if err := conn.WriteMessage(websocket.TextMessage, userJ); err != nil {
							log.Println("websocket write message error:", err)
						}
					} else {
						// received eof or else
						return
					}
				}
			}
		}()

		//debug
		log.Println("after end channel send")
		wg.Wait()
	}
}
