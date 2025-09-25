package main

import (
	"bufio"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"

	"github.com/gorilla/websocket"
)

var u = websocket.Upgrader{}

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		server()
	} else {
		fmt.Println("client")
		client()
	}
}

func client() {
	u := url.URL{Scheme: "ws", Host: "localhost:8888", Path: "/socket"}

	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()

	done := make(chan struct{}) // 読み取りゴルーチンの終了を通知
	stop := make(chan struct{}) // 書き込みゴルーチンに終了を指示

	// サーバーからのメッセージを読み取るゴルーチン
	go func() {
		defer close(done) // 終了時にdoneチャネルを閉じる
		for {
			_, message, err := c.ReadMessage()
			if err != nil {
				log.Println("read:", err)
				return
			}
			log.Printf("recv: %s", message)
		}
	}()

	// 標準入力からユーザーの入力を処理するゴルーチン
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			// stopチャネルからのシグナルを待つ
			select {
			case <-stop:
				log.Println("stdin goroutine stopping...")
				return
			default:
				// ブロックしないようにデフォルトケースを使用
			}

			line, err := reader.ReadString('\n')
			if err != nil {
				log.Println("stdin read error:", err)
				return
			}

			err = c.WriteMessage(websocket.TextMessage, []byte(line))
			if err != nil {
				log.Println("write:", err)
				return
			}
		}
	}()

	// 正常なシャットダウン処理
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case <-done:
			// サーバーからの読み取りゴルーチンが終了した場合
			log.Println("Read goroutine finished.")
			return
		case <-interrupt:
			// シャットダウンシグナルを受信した場合
			log.Println("interrupt")
			close(stop) // 書き込みゴルーチンに停止を指示
			err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Println("write close:", err)
			}
			// サーバーからの読み取りゴルーチンが終了するのを待つ
			<-done
			return
		}
	}
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
