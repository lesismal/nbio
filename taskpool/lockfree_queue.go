// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package taskpool

import (
	"sync/atomic"
	"unsafe"
)

type LockFreeNode struct {
	value interface{}
	next  unsafe.Pointer // *LockFreeNode
}

type LockFreeQueue struct {
	head unsafe.Pointer // *LockFreeNode
	tail unsafe.Pointer // *LockFreeNode
	size int64
}

func NewLockFreeQueue() *LockFreeQueue {
	node := &LockFreeNode{}
	ptr := unsafe.Pointer(node)
	return &LockFreeQueue{
		head: ptr,
		tail: ptr,
	}
}

func (q *LockFreeQueue) Enqueue(value interface{}) {
	node := &LockFreeNode{value: value}
	nodePtr := unsafe.Pointer(node)

	for {
		tailPtr := atomic.LoadPointer(&q.tail)
		tail := (*LockFreeNode)(tailPtr)
		tailNext := atomic.LoadPointer(&tail.next)

		if tailPtr == atomic.LoadPointer(&q.tail) {
			if tailNext == nil {
				if atomic.CompareAndSwapPointer(&tail.next, nil, nodePtr) {
					atomic.CompareAndSwapPointer(&q.tail, tailPtr, nodePtr)
					atomic.AddInt64(&q.size, 1)
					return
				}
			} else {
				atomic.CompareAndSwapPointer(&q.tail, tailPtr, tailNext)
			}
		}
	}
}

func (q *LockFreeQueue) Dequeue() (interface{}, bool) {
	for {
		headPtr := atomic.LoadPointer(&q.head)
		head := (*LockFreeNode)(headPtr)
		tailPtr := atomic.LoadPointer(&q.tail)
		headNext := atomic.LoadPointer(&head.next)

		if headPtr == atomic.LoadPointer(&q.head) {
			if headPtr == tailPtr {
				if headNext == nil {
					return nil, false
				}
				atomic.CompareAndSwapPointer(&q.tail, tailPtr, headNext)
			} else {
				next := (*LockFreeNode)(headNext)
				value := next.value
				if atomic.CompareAndSwapPointer(&q.head, headPtr, headNext) {
					atomic.AddInt64(&q.size, -1)
					return value, true
				}
			}
		}
	}
}

func (q *LockFreeQueue) IsEmpty() bool {
	headPtr := atomic.LoadPointer(&q.head)
	head := (*LockFreeNode)(headPtr)
	return atomic.LoadPointer(&head.next) == nil
}

func (q *LockFreeQueue) Size() int64 {
	return atomic.LoadInt64(&q.size)
}

func (q *LockFreeQueue) Clear() {
	node := &LockFreeNode{}
	ptr := unsafe.Pointer(node)
	atomic.StorePointer(&q.head, ptr)
	atomic.StorePointer(&q.tail, ptr)
	atomic.StoreInt64(&q.size, 0)
}
