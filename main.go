package main

import gopack "github.com/codemeow5/GoPack/lib"
import "time"
import "fmt"

type CallbackObj struct {
}

func (cb *CallbackObj) Invoke(payload []byte, err error) {
	if err == nil {
		fmt.Println(string(payload))
	}
}

func main() {
	cb := new(CallbackObj)

	opts := gopack.Options{
		Address:     "127.0.0.1:8080",
		CallbackObj: cb,
	}

	gopk, err := gopack.NewGoPack(&opts)
	if err != nil {
		return
	}

	gopk.Start()

	// payload := bytes.NewBufferString("First message!").Bytes()
	// gopk.Commit(payload, 0)
	// payload = bytes.NewBufferString("Second message! (Qos 1)").Bytes()
	// gopk.Commit(payload, 1)
	// payload = bytes.NewBufferString("Third message! (Qos 2)").Bytes()
	// gopk.Commit(payload, 2)

	for {
		time.Sleep(10 * time.Second)
	}
}
