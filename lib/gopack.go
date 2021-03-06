package gopack

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// ErrMissingParams missing parameters error
var ErrMissingParams = errors.New("missing parameters")

// GoPack2 GoPack2 main class
// naming GoPack2 instead of GoPack to compatible with gomobile bind
type GoPack2 struct {
	opts      *Options
	conn      *net.TCPConn
	errCh     chan error
	exitCh    chan struct{}
	waitGroup sync.WaitGroup
}

// StorageInterface storage class implementation
type StorageInterface interface {
	UniqueID() int
	Save(*Packet)
	Unconfirmed() *Packet
	Confirm(int) *Packet
	Receive(int, []byte)
	Release(int) []byte
}

// GoCallback be used to receive callback
type GoCallback interface {
	Invoke([]byte, error)
}

// Options GoPack2 create options
type Options struct {
	Address         string
	CallbackObj     GoCallback
	MaxPacketNumber int
	Storage         StorageInterface
	Heartbeat       int
}

// NewGoPack creates and initializes a new GoPack2 using opts
func NewGoPack(opts *Options) (gopack *GoPack2, err error) {
	if opts == nil ||
		opts.CallbackObj == nil {
		return nil, ErrMissingParams
	}
	if opts.MaxPacketNumber == 0 {
		opts.MaxPacketNumber = 20
	}
	if opts.Heartbeat == 0 {
		opts.Heartbeat = 1000
	}
	if opts.Storage == nil {
		opts.Storage = newMemoryStorage()
	}
	gopack = &GoPack2{opts: opts}
	return gopack, nil
}

func (gopack *GoPack2) cbErr(err error) {
	gopack.opts.CallbackObj.Invoke(nil, err)
}

func (gopack *GoPack2) readPacket() (packet *Packet, err error) {
	buffer := make([]byte, 5)
	_, err = io.ReadFull(gopack.conn, buffer)
	if err != nil {
		return nil, err
	}
	num := buffer[3:]
	remainingLength := binary.BigEndian.Uint16(num)
	payload := make([]byte, remainingLength)
	_, err = io.ReadFull(gopack.conn, payload)
	if err != nil {
		return nil, err
	}
	buffer = append(buffer, payload...)
	return Decode(buffer)
}

func (gopack *GoPack2) read() {
	defer gopack.waitGroup.Done()
	for {
		select {
		case <-gopack.exitCh:
			return
		default:
			packet, err := gopack.readPacket()
			if err != nil {
				gopack.errCh <- err
				return
			}
			gopack.handle(packet)
		}
	}
}

func (gopack *GoPack2) retry(packet *Packet) (retryPacket *Packet) {
	if packet.Qos == Qos0 {
		return nil
	}
	if packet.RetryTimes > 0 {
		retryPacket = packet.Clone()
		retryPacket.RetryTimes++
		retryPacket.Timestamp = time.Now().Add(
			time.Duration(5*retryPacket.RetryTimes) * time.Second).Unix()
	} else {
		retryPacket = Encode(packet.MsgType, packet.Qos, 1, packet.MsgID, packet.Payload)
		retryPacket.RetryTimes = 1
		retryPacket.Timestamp = time.Now().Add(
			time.Duration(5*retryPacket.RetryTimes) * time.Second).Unix()
	}
	return retryPacket
}

func (gopack *GoPack2) write() {
	defer gopack.waitGroup.Done()
	for {
		select {
		case <-gopack.exitCh:
			return
		default:
			packet := gopack.opts.Storage.Unconfirmed()
			if packet == nil {
				time.Sleep(time.Duration(gopack.opts.Heartbeat) * time.Millisecond)
				continue
			}
			retryPacket := gopack.retry(packet)
			if retryPacket != nil {
				gopack.opts.Storage.Save(retryPacket)
			}
			_, err := gopack.conn.Write(packet.Buffer)
			if err != nil {
				gopack.errCh <- err
				return
			}
		}
	}
}

func (gopack *GoPack2) handle(packet *Packet) {
	if packet.MsgType == MsgTypeSend {
		if packet.Qos == Qos0 {
			gopack.opts.CallbackObj.Invoke(packet.Payload, nil)
		} else if packet.Qos == Qos1 {
			reply := Encode(MsgTypeAck, Qos0, 0, packet.MsgID, nil)
			gopack.opts.Storage.Save(reply)
			gopack.opts.CallbackObj.Invoke(packet.Payload, nil)
		} else if packet.Qos == Qos2 {
			gopack.opts.Storage.Receive(packet.MsgID, packet.Payload)
			reply := Encode(MsgTypeReceived, Qos0, 0, packet.MsgID, nil)
			gopack.opts.Storage.Save(reply)
		}
	} else if packet.MsgType == MsgTypeAck {
		gopack.opts.Storage.Confirm(packet.MsgID)
	} else if packet.MsgType == MsgTypeReceived {
		gopack.opts.Storage.Confirm(packet.MsgID)
		reply := Encode(MsgTypeRelease, Qos1, 0, packet.MsgID, nil)
		gopack.opts.Storage.Save(reply)
	} else if packet.MsgType == MsgTypeRelease {
		payload := gopack.opts.Storage.Release(packet.MsgID)
		if payload != nil {
			gopack.opts.CallbackObj.Invoke(payload, nil)
		}
		reply := Encode(MsgTypeCompleted, Qos0, 0, packet.MsgID, nil)
		gopack.opts.Storage.Save(reply)
	} else if packet.MsgType == MsgTypeCompleted {
		gopack.opts.Storage.Confirm(packet.MsgID)
	}
}

// Commit is used to commit message to GoPack2
func (gopack *GoPack2) Commit(payload []byte, qos byte) {
	packet := Encode(MsgTypeSend, qos, 0, gopack.opts.Storage.UniqueID(), payload)
	gopack.opts.Storage.Save(packet)
}

// Start internal connection loop
func (gopack *GoPack2) Start() {
	go gopack.Conn()
}

// Conn internal connection loop (synchronization)
func (gopack *GoPack2) Conn() {
	for {
		conn, err := net.DialTimeout("tcp", gopack.opts.Address, 2*time.Second)
		if err != nil {
			gopack.cbErr(err)
		} else {
			gopack.conn = conn.(*net.TCPConn)
			gopack.exitCh = make(chan struct{})
			gopack.errCh = make(chan error, 2)
			gopack.waitGroup.Add(2)
			go gopack.read()
			go gopack.write()
			err = <-gopack.errCh
			close(gopack.exitCh)
			gopack.waitGroup.Wait()
			close(gopack.errCh)
			if err != nil {
				gopack.cbErr(err)
			}
		}
		if conn != nil {
			conn.Close()
		}
		gopack.conn = nil
		time.Sleep(3 * time.Second)
	}
}
