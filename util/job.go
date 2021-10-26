package util

import (
	"sync"
	"time"

	"github.com/enriquebris/goconcurrentqueue"
)

/*
A job wraps an input and many outputs
*/

var Pin sync.WaitGroup

type Job struct {
	Input   Outputs
	inout   InOut
	outputs Outputs

	hot sync.Map

	_id     func(interface{}) string
	_worker func(...interface{})

	Disable bool
}

func (self *Job) Set(n int) {
	//inputs and outputs
	self.Input.Set(n)
	self.inout.Set(n)
	self.outputs.Set(n)
}

func (self *Job) Add(v Blockable) {
	self.outputs.Add(v)
}

func (self *Job) Plumb(values ...interface{}) {
	if self.Disable {
		return
	}

	if self._id != nil {
		var i = 0
		for _, v := range values {
			_, loaded := self.hot.LoadOrStore(self._id(v), false)
			if !loaded {
				values[i] = v
				i++
			}
		}
		values = values[0:i]
	}
	self.outputs.Set(len(values)) //only outputs
	self.inout.Plumb(values...)
}

func (self *Job) Spinup(worker func(...interface{}), n int) {
	self.inout.queue = goconcurrentqueue.NewFIFO()
	self._worker = worker

	foo := func(values []interface{}) {
		if self._id != nil {
			for _, v := range values {
				id := self._id(v)
				self.hot.Store(id, true)
				defer self.hot.Delete(id)
			}
		}
		worker(values...)
		self.outputs.Set(-len(values))
	}

	go func() {
		Pin.Wait()
		for {
			values := self.inout.PollN(1, n)
			foo(values)
		}
	}()
}

func (self *Job) FilterRepeats(id func(interface{}) string) {
	self._id = id
}

type WaitJob struct {
	Job
	waiter Block
}

func (self *WaitJob) Spinup(worker func(...interface{}), n int) {
	self.Job.Input.Add(&self.waiter)

	self.Job.Spinup(func(v ...interface{}) {
		self.waiter.Wait()
		worker(v...)
	}, n)
}

type TriggerJob struct {
	WaitJob
}

func (self *TriggerJob) Trigger() {
	self.Plumb("")
}

func (self *TriggerJob) Spinup(worker func()) {
	bar := func(_ interface{}) string { return "" }
	self.FilterRepeats(bar)
	foo := func(_ ...interface{}) {
		//because trigger jobs are normally state dependant... once we start, any triggers mean what we are doing will be out of data!
		//this line will prevent repeat filtering after this point
		self.hot.Delete("")

		worker()
	}
	self.WaitJob.Spinup(foo, 1)
}

type CooldownJob struct {
	TriggerJob
}

func (self *CooldownJob) Spinup(worker func(), n int) {
	foo := func() {
		self.hot.Delete("")
		worker()
		time.Sleep(time.Second * time.Duration(n))
	}
	self.TriggerJob.Spinup(foo)
}

/*
This plumbs into other stuff syncronously
lookin kinda empty
Its just a input and output with no Inbetween
*/
type Delegate struct {
	Inputs  Outputs
	outputs Outputs
	Foo     func(interface{})
}

func (self *Delegate) Add(v Blockable) {
	self.outputs.Add(v)
}

func (self *Delegate) AddJob(v Output) {
	self.Inputs.Add(v)
	v.Add(&self.outputs)
}

func (self *Delegate) Set(n int) {
	self.Inputs.Set(n)
	self.outputs.Set(n)
}

func (self *Delegate) Plumb(values ...interface{}) {
	for _, v := range values {
		self.Foo(v)
	}
}

func Depends(source Output, sink Blockable) {
	source.Add(sink)
}
