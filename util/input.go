/*
An input implements an interface for connecting things:
*/
package util

import "github.com/enriquebris/goconcurrentqueue"

type Input interface {
	Blockable
	Plumb(...interface{})
}

type InOut struct {
	block Block
	queue goconcurrentqueue.Queue
}

func (self *InOut) Set(n int) {
	self.block.Set(n)
}

func (self *InOut) Plumb(values ...interface{}) {
	for _, v := range values {
		self.queue.Enqueue(v)
	}
	self.block.Trigger()
}

func (self *InOut) PollN(min, max int) []interface{} {
	var ret = make([]interface{}, max)
	var i int
	for i = 0; i < max; i++ {
		v, err := self.Poll()
		if err {
			if i < min {
				//wait for at least min items
				v, e := self.queue.DequeueOrWaitForNextElement()
				if e != nil {
					panic("wtf")
				} else {
					ret[i] = v
				}
			} else {
				break
			}
		} else {
			ret[i] = v
		}
	}
	return ret[0:i]
}

func (self *InOut) Poll() (interface{}, bool) {
	for {
		v, err := self.queue.Dequeue()
		if err == nil {
			return v, false
		}

		i := self.block.Wait()
		if i == 0 {
			//closed
			return nil, true
		}
	}
}
