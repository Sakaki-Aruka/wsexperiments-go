package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
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

	stdoutScanner := bufio.NewScanner(stdout)
	sig := make(chan os.Signal)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGKILL, os.Interrupt)

	go func() {
		for stdoutScanner.Scan() {
			t := stdoutScanner.Text()
			fmt.Println(t)
		}
	}()

	go func() {
		select {
		case s := <-sig:
			if err := cmd.Process.Signal(s); err != nil {
				fmt.Println("kill error: " + err.Error())
			}
		}
	}()

	if err := cmd.Start(); err != nil {
		fmt.Println("process start error: " + err.Error())
		return
	}
	cmd.Process.Wait()
}
