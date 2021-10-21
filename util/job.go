package util

import (
	"sync"
	"sync/atomic"
	"time"
)

type Job struct {
	cond  sync.Cond
	queue []string

	hot sync.Map
	//die bool TODO killable

	pause  int32 // 0 to unpause
	active uint32

	idle sync.Cond
}

func (job *Job) Spinup(worker func([]string), batchsize int, maxparallel uint32, permithot bool) {
	job.queue = make([]string, 0, 50)
	job.cond = *sync.NewCond(&sync.Mutex{})
	job.idle = *sync.NewCond(&sync.Mutex{})
	//job.Lock is signaled every time Ids is altered
	//we want to start the worker function any time
	//A: no workers are running and there is at least one thing in the queue
	//or B: the queue has batchsize items

	foo := func(batch []string) {
		if permithot {
			for _, s := range batch {
				job.hot.Delete(s)
			}
		}
		worker(batch) //must be blocking

		//the idle cond really messing stuff up
		if atomic.LoadUint32(&job.active) == 1 {
			empty := job.None()

			job.idle.L.Lock()
			a := atomic.AddUint32(&job.active, ^uint32(0)) //decrement
			if a == 0 && empty {
				job.idle.Broadcast()
			}
			job.idle.L.Unlock()
		} else {
			atomic.AddUint32(&job.active, ^uint32(0)) //decrement
		}

		if !permithot {
			for _, s := range batch {
				job.hot.Delete(s)
			}
		}
		if atomic.LoadInt32(&job.pause) == 0 {
			job.cond.Broadcast() //wakeup Wait in for loop
		}
	}

	go func() {
		job.cond.L.Lock() //must start locked

		for {
			pause := atomic.LoadInt32(&job.pause) > 0
			if !pause && len(job.queue) > 0 {
				a := atomic.LoadUint32(&job.active)
				if len(job.queue) > batchsize {
					if a < maxparallel {
						atomic.AddUint32(&job.active, 1)
						go foo(job.queue[:batchsize])
						job.queue = job.queue[batchsize:]
						continue
					}
				} else if a == 0 {
					atomic.AddUint32(&job.active, 1)
					go foo(job.queue[:]) //pretty sure the next line WONT undo this
					job.queue = make([]string, 0, 50)
				}
			}
			job.cond.Wait()
		}
	}()
}

func (job *Job) Add(id string) {
	_, hot := job.hot.LoadOrStore(id, true)
	if !hot {
		job.cond.L.Lock()
		job.queue = append(job.queue, id)
		job.cond.L.Unlock()

		// only need to wakeup if we are not paused
		if atomic.LoadInt32(&job.pause) == 0 {
			job.cond.Broadcast() //wakeup spinup
		}
	}
}

//added in order to prevent unnessecary small batches
func (job *Job) Pause(pause bool) {
	if pause {
		atomic.AddInt32(&job.pause, 1)
	} else {
		ret := atomic.AddInt32(&job.pause, -1)
		if ret < 0 {
			panic("Job: call to unpause without call to pause")
		}
		if ret == 0 && len(job.queue) > 0 {
			job.cond.Broadcast() //wakeup spinup
		}
	}
}

// For cooldown type jobs,
// TODO probably rewrite this in a more sensible way
type CooldownJob = Job

func (job *CooldownJob) Trigger() {
	job.Add("")
}

func (job *CooldownJob) SpinupCooldown(foo func(), delay time.Duration) {
	job.Spinup(func([]string) {
		foo()
		time.Sleep(delay)
	}, 1, 1, true)
}

// For the playlist task
func (job *Job) None() bool {
	job.cond.L.Lock()
	a := len(job.queue)
	job.cond.L.Unlock()
	return a == 0
}

// For the playlist task
func (job *Job) IsIdle() bool {
	return atomic.LoadUint32(&job.active) == 0
}

func WaitOnIdle(jobs ...*Job) {
OUTER:
	for {
		for _, j := range jobs {
			j.WaitOnIdle()
		}
		for _, j := range jobs {
			if !j.IsIdle() && j.None() {
				continue OUTER
			}
		}
		break
	}
}

func (job *Job) WaitOnIdle() {
	job.idle.L.Lock()
	for {
		if job.IsIdle() {
			job.idle.L.Unlock()
			if job.None() && job.IsIdle() {
				return
			} else {
				job.idle.L.Lock()
				continue
			}
		}
		job.idle.Wait()
	}
}
