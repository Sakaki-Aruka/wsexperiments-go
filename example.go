package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

// WebSocketのアップグレード設定
var upg = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// すべてのオリジンからの接続を許可（本番環境ではセキュリティを考慮して制限すべきです）
		return true
	},
}

type req struct {
	Env  []string `json:"env"`
	Line string   `json:"line"`
	Pwd  string   `json:"pwd"`
}

// WebSocket接続を処理するハンドラー
func wsHandler(w http.ResponseWriter, r *http.Request) {
	conn, err := upg.Upgrade(w, r, nil)
	if err != nil {
		log.Println("WebSocket Upgrade Error:", err)
		return
	}
	defer conn.Close()

	log.Println("New client connected.")

	// コマンド実行状態を保持する変数
	var cmd *exec.Cmd
	var stdinPipe *os.File
	var stdoutPipe *os.File
	var stderrPipe *os.File
	var processRunning bool
	var wg sync.WaitGroup

	// 接続が切断されたとき、実行中のプロセスを停止するための関数
	cleanup := func() {
		if processRunning && cmd != nil && cmd.Process != nil {
			log.Println("Terminating running process...")
			// SIGINTを送信してGracefulに終了を試みる
			if err := cmd.Process.Signal(os.Interrupt); err != nil {
				log.Println("Error sending SIGINT:", err)
				// 強制終了
				if err := cmd.Process.Kill(); err != nil {
					log.Println("Error killing process:", err)
				}
			}
			// コマンドの終了を待つ
			wg.Wait()
			log.Println("Process terminated.")
		}
		processRunning = false
	}
	defer cleanup()

	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Println("Client disconnected gracefully.")
			} else {
				log.Println("Read Error:", err)
			}
			break
		}

		if messageType == websocket.TextMessage {
			var r req
			if err := json.Unmarshal(message, &r); err != nil {
				log.Println("json parse error:", err)
				continue
			}
			input := r.Line
			input = strings.TrimSuffix(input, "\n")
			log.Printf("Received: %s\n", input)

			if !processRunning {
				// プロセスが実行中でない場合、新しいコマンドとして解釈する
				args := strings.Split(input, " ") //splitCommand(input) // コマンドと引数を分割する（単純なスペース区切り）
				if len(args) == 0 {
					conn.WriteMessage(websocket.TextMessage, []byte("Error: Empty command received."))
					continue
				}

				commandName := args[0]
				commandArgs := args[1:]

				cmd = exec.Command(commandName, commandArgs...)
				cmd.Env = r.Env
				cmd.Dir = r.Pwd

				// 標準入力パイプを取得
				stdin, err := cmd.StdinPipe()
				if err != nil {
					log.Println("Error getting StdinPipe:", err)
					conn.WriteMessage(websocket.TextMessage, []byte("Error setting up input pipe."))
					continue
				}
				stdinPipe = stdin.(*os.File)

				// 標準出力パイプを取得
				stdout, err := cmd.StdoutPipe()
				if err != nil {
					log.Println("Error getting StdoutPipe:", err)
					conn.WriteMessage(websocket.TextMessage, []byte("Error setting up output pipe."))
					stdinPipe.Close()
					continue
				}
				stdoutPipe = stdout.(*os.File)

				// 標準エラーパイプを取得
				stderr, err := cmd.StderrPipe()
				if err != nil {
					log.Println("Error getting StderrPipe:", err)
					conn.WriteMessage(websocket.TextMessage, []byte("Error setting up error pipe."))
					stdinPipe.Close()
					stdoutPipe.Close()
					continue
				}
				stderrPipe = stderr.(*os.File)

				// コマンドの実行を開始
				if err := cmd.Start(); err != nil {
					log.Println("Error starting command:", err)
					conn.WriteMessage(websocket.TextMessage, []byte("Error starting command: "+err.Error()))
					stdinPipe.Close()
					stdoutPipe.Close()
					stderrPipe.Close()
					continue
				}
				processRunning = true
				log.Printf("Command started: %s\n", input)
				conn.WriteMessage(websocket.TextMessage, []byte("Command started successfully. Sending subsequent messages as input."))

				// --- 標準出力の読み取りとWebSocketへの送信 ---
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer stdoutPipe.Close()
					readAndSendOutput(conn, stdoutPipe, "STDOUT")
					log.Println("STDOUT reader finished.")
				}()

				// --- 標準エラー出力の読み取りとWebSocketへの送信 ---
				wg.Add(1)
				go func() {
					defer wg.Done()
					defer stderrPipe.Close()
					readAndSendOutput(conn, stderrPipe, "STDERR")
					log.Println("STDERR reader finished.")
				}()

				// --- コマンド終了の待機 ---
				wg.Add(1)
				go func() {
					defer wg.Done()
					// コマンドの終了を待つ
					err := cmd.Wait()
					processRunning = false

					// Waitが返る前にconn.WriteMessageが呼ばれるとパニックになる可能性があるので、
					// conn.WriteMessageは排他制御を行うか、ここで接続を切断します
					// 簡単のために、終了メッセージを送って接続を閉じます。
					exitMessage := "Command finished."
					if err != nil {
						exitMessage = "Command finished with error: " + err.Error()
					}
					log.Println(exitMessage)

					// 終了メッセージの送信
					if writeErr := conn.WriteMessage(websocket.TextMessage, []byte(exitMessage)); writeErr != nil {
						log.Println("Error writing exit message:", writeErr)
					}
					// プロセスが終了したので、WebSocket接続も終了させます
					conn.Close()
				}()

			} else {
				// プロセスが実行中の場合、入力をプロセスの標準入力に書き込む
				if !strings.HasSuffix(r.Line, "\n") {
					r.Line = r.Line + "\n"
				}
				_, err := stdinPipe.Write([]byte(r.Line)) // 通常、コマンドライン入力は改行で区切られます
				if err != nil {
					log.Println("Error writing to process stdin:", err)
					conn.WriteMessage(websocket.TextMessage, []byte("Error writing input to process."))
					// プロセスを停止し、接続を閉じる
					cleanup()
					break
				}
			}
		}
	}
	// ループを抜けた後、defer cleanup()が呼ばれる
}

// readAndSendOutput は、プロセスからの出力を読み込み、WebSocket接続に送信します。
func readAndSendOutput(conn *websocket.Conn, reader *os.File, prefix string) {
	buffer := make([]byte, 1024)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			output := buffer[:n]
			// 出力にプレフィックスを付けて送信
			message := []byte("[" + prefix + "] " + string(output))
			if err := conn.WriteMessage(websocket.TextMessage, message); err != nil {
				log.Println("Write Error:", err)
				return
			}
		}
		if err != nil {
			// EOFまたはその他のエラー
			if err.Error() != "EOF" {
				log.Println("Read Error from process pipe:", err)
			}
			return
		}
	}
}

// splitCommand は、単純なスペース区切りでコマンドと引数を分割します。
// 実際のシェル挙動（引用符、エスケープなど）を完全に再現するわけではありません。
func splitCommand(command string) []string {
	var args []string
	var current string
	for _, char := range command {
		if char == ' ' || char == '\t' {
			if current != "" {
				args = append(args, current)
				current = ""
			}
		} else {
			current += string(char)
		}
	}
	if current != "" {
		args = append(args, current)
	}
	return args
}

// main関数
func main() {
	// ログ設定
	log.SetFlags(log.Ldate | log.Ltime | log.Lshortfile)

	// WebSocketエンドポイントの定義
	http.HandleFunc("/ws", wsHandler)

	log.Println("Starting server on :8080")
	// HTTPサーバーの起動
	err := http.ListenAndServe(":8080", nil)
	if err != nil {
		log.Fatal("ListenAndServe: ", err)
	}
}
