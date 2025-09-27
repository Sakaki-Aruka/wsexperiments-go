package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
)

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		return
	}

	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = os.Environ()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Println("failed to get stdout pipe")
		return
	}
	stdoutScanner := bufio.NewReader(stdout)
	cmd.Stdin = os.Stdin
	sig := make(chan os.Signal)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGKILL, os.Interrupt)
	done := make(chan struct{})

	if err := cmd.Start(); err != nil {
		fmt.Println("process start error: " + err.Error())
		return
	}

	outChannel := make(chan []byte)
	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer func() {
			close(outChannel)
			wg.Done()
		}()
		buf := make([]byte, 4096)
		for {
			n, err := stdoutScanner.Read(buf)
			if n > 0 {
				d := buf[:n]
				outChannel <- d
			}

			if err != nil {
				fmt.Println("read error:", err)
				break
			}
		}
		done <- struct{}{}
	}()

	go func() {
		defer wg.Done()
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
				fmt.Print(string(d))
			}
		}
	}()

	go func() {
		defer wg.Done()
		select {
		case s := <-sig:
			if err := cmd.Process.Signal(s); err != nil {
				fmt.Println("kill error: " + err.Error())
			}
		case <-done:
			return
		}
	}()

	wg.Wait()
}
