package allocator

import (
	"os"
	"path"
	"sync"

	"github.com/edsrzf/mmap-go"
	"github.com/missionMeteora/journaler"
	"github.com/missionMeteora/toolkit/errors"
)

// NewMMap will return a new Mmap
func NewMMap(dir, name string) (mp *MMap, err error) {
	var m MMap
	if m.f, err = os.OpenFile(path.Join(dir, name), os.O_CREATE|os.O_RDWR, 0644); err != nil {
		return
	}

	mp = &m
	return
}

// MMap manages the memory mapped file
type MMap struct {
	mux sync.RWMutex

	f  *os.File
	mm mmap.MMap
	fl freelist

	tail int64
	cap  int64

	onGrow []OnGrowFn
}

func (m *MMap) unmap() (err error) {
	if m.mm == nil {
		return
	}

	return m.mm.Unmap()
}

// row will grow the underlying MMap file
func (m *MMap) grow(sz int64) (grew bool) {
	// TODO: Make m.cap atomic
	if m.cap > sz {
		return
	}

	m.mux.Lock()
	defer m.mux.Unlock()

	var err error
	if m.cap == 0 {
		var fi os.FileInfo
		if fi, err = m.f.Stat(); err != nil {
			journaler.Error("Stat error: %v", err)
			return
		}

		if m.cap = fi.Size(); m.cap == 0 {
			m.cap = sz
		}
	}

	for m.cap <= sz {
		m.cap *= 2
	}

	if err = m.unmap(); err != nil {
		journaler.Error("Unmap error: %v", err)
		return
	}

	if err = m.f.Truncate(m.cap); err != nil {
		journaler.Error("Truncate error: %v", err)
		return
	}

	if m.mm, err = mmap.Map(m.f, os.O_RDWR, 0); err != nil {
		journaler.Error("Map error: %v", err)
		return
	}

	return true
}

func (m *MMap) notify() {
	m.mux.RLock()
	defer m.mux.RUnlock()

	for _, fn := range m.onGrow {
		fn()
	}
}

// Grow will grow the underlying MMap file
func (m *MMap) Grow(sz int64) (grew bool) {
	if grew = m.grow(sz); grew {
		m.notify()
	}

	return
}

// EnsureSize will ensure the tail is at least at the requested size or greater
func (m *MMap) EnsureSize(sz int64) (grew bool) {
	// TODO: Make tail atomic
	if m.tail >= sz {
		return
	}

	m.tail = sz
	return m.grow(sz)
}

// Get will get bytes
func (m *MMap) Get(offset, sz int64) []byte {
	m.mux.RLock()
	defer m.mux.RUnlock()

	return m.mm[offset : offset+sz]
}

// Allocate will allocate bytes
func (m *MMap) Allocate(sz int64) (s Section, grew bool) {
	s.Size = sz
	if s.Offset = m.fl.acquire(sz); s.Offset != -1 {
		return
	}

	s.Offset = m.tail
	m.tail += sz
	grew = m.Grow(m.tail)
	return
}

// Release will release a section
func (m *MMap) Release(s Section) {
	m.fl.release(s)
	return
}

// OnGrow will append a function to be called on grows
func (m *MMap) OnGrow(fn OnGrowFn) {
	m.onGrow = append(m.onGrow, fn)
}

// Close will close an MMap
func (m *MMap) Close() (err error) {
	m.mux.Lock()
	defer m.mux.Unlock()

	if m.f == nil {
		return errors.ErrIsClosed
	}

	var errs errors.ErrorList
	errs.Push(m.mm.Flush())
	errs.Push(m.mm.Unmap())
	errs.Push(m.f.Close())
	m.f = nil
	return
}
