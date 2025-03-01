// Copyright 2020 lesismal. All rights reserved.
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package taskpool

import (
	"sync/atomic"
	"unsafe"
)

// LockFreeNode 表示无锁队列中的节点
type LockFreeNode struct {
	value interface{}
	next  unsafe.Pointer // *LockFreeNode
}

// LockFreeQueue 是一个无锁队列的实现
// 基于Michael-Scott无锁队列算法
type LockFreeQueue struct {
	head unsafe.Pointer // *LockFreeNode
	tail unsafe.Pointer // *LockFreeNode
	size int64
}

// NewLockFreeQueue 创建一个新的无锁队列
func NewLockFreeQueue() *LockFreeQueue {
	node := &LockFreeNode{}
	ptr := unsafe.Pointer(node)
	return &LockFreeQueue{
		head: ptr,
		tail: ptr,
	}
}

// Enqueue 将一个值添加到队列尾部
func (q *LockFreeQueue) Enqueue(value interface{}) {
	node := &LockFreeNode{value: value}
	nodePtr := unsafe.Pointer(node)

	for {
		tailPtr := atomic.LoadPointer(&q.tail)
		tail := (*LockFreeNode)(tailPtr)
		tailNext := atomic.LoadPointer(&tail.next)

		// 检查tail是否仍然是尾节点
		if tailPtr == atomic.LoadPointer(&q.tail) {
			if tailNext == nil {
				// 尝试添加新节点
				if atomic.CompareAndSwapPointer(&tail.next, nil, nodePtr) {
					// 成功添加，尝试更新tail指针
					atomic.CompareAndSwapPointer(&q.tail, tailPtr, nodePtr)
					atomic.AddInt64(&q.size, 1)
					return
				}
			} else {
				// tail不是真正的尾节点，帮助更新tail指针
				atomic.CompareAndSwapPointer(&q.tail, tailPtr, tailNext)
			}
		}
	}
}

// Dequeue 从队列头部移除并返回一个值
// 如果队列为空，返回nil和false
func (q *LockFreeQueue) Dequeue() (interface{}, bool) {
	for {
		headPtr := atomic.LoadPointer(&q.head)
		head := (*LockFreeNode)(headPtr)
		tailPtr := atomic.LoadPointer(&q.tail)
		headNext := atomic.LoadPointer(&head.next)

		// 检查head是否仍然是头节点
		if headPtr == atomic.LoadPointer(&q.head) {
			// 如果head和tail相同，队列可能为空或者tail落后了
			if headPtr == tailPtr {
				if headNext == nil {
					// 队列为空
					return nil, false
				}
				// tail落后了，帮助更新tail指针
				atomic.CompareAndSwapPointer(&q.tail, tailPtr, headNext)
			} else {
				// 队列不为空，获取值并尝试移除头节点
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

// IsEmpty 检查队列是否为空
func (q *LockFreeQueue) IsEmpty() bool {
	headPtr := atomic.LoadPointer(&q.head)
	head := (*LockFreeNode)(headPtr)
	return atomic.LoadPointer(&head.next) == nil
}

// Size 返回队列中的元素数量
func (q *LockFreeQueue) Size() int64 {
	return atomic.LoadInt64(&q.size)
}

// Clear 清空队列
func (q *LockFreeQueue) Clear() {
	node := &LockFreeNode{}
	ptr := unsafe.Pointer(node)
	atomic.StorePointer(&q.head, ptr)
	atomic.StorePointer(&q.tail, ptr)
	atomic.StoreInt64(&q.size, 0)
}
