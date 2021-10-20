package util

import "sync"

func Batched(foo func(int, []string), ls []string, n int, async bool) {
	var wg sync.WaitGroup
	bar := func(offset int, idss []string) {
		foo(offset, idss)
		wg.Done()
	}

	for i := 0; i < len(ls); i += n {
		if n > len(ls)-i {
			n = len(ls) - i
		}
		wg.Add(1)
		if async {
			go bar(i, ls[i:i+n])
		} else {
			foo(i, ls[i:i+n])
		}
	}
	if async {
		wg.Wait()
	}
}
