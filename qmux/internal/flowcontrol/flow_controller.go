package flowcontrol

import (
	"sync"
)

// FlowController manages the flow control window.
type FlowController struct {
	mutex sync.Mutex

	receiveWindow      uint64
	maxReceiveWindow   uint64
	receivedBytes      uint64
	readBytes          uint64

	sendWindow         uint64
	sentBytes          uint64
	
	// wake is closed when the send window is updated.
	// It is replaced with a new channel after closure.
	wake chan struct{}
}

// NewFlowController creates a new flow controller.
func NewFlowController(initialWindow uint64) *FlowController {
	return &FlowController{
		receiveWindow:    initialWindow,
		maxReceiveWindow: initialWindow,
		sendWindow:       initialWindow,
		wake:             make(chan struct{}),
	}
}

// UpdateSendWindow updates the send window.
func (fc *FlowController) UpdateSendWindow(limit uint64) {
	fc.mutex.Lock()
	defer fc.mutex.Unlock()
	if limit > fc.sendWindow {
		fc.sendWindow = limit
		close(fc.wake)
		fc.wake = make(chan struct{})
	}
}

// AddSentBytes adds sent bytes and checks if it's within the window.
func (fc *FlowController) AddSentBytes(n uint64) bool {
	fc.mutex.Lock()
	defer fc.mutex.Unlock()
	if fc.sentBytes+n > fc.sendWindow {
		return false
	}
	fc.sentBytes += n
	return true
}

// SendWindowRemaining returns the remaining send window.
func (fc *FlowController) SendWindowRemaining() uint64 {
	fc.mutex.Lock()
	defer fc.mutex.Unlock()
	if fc.sentBytes >= fc.sendWindow {
		return 0
	}
	return fc.sendWindow - fc.sentBytes
}

// WaitSendWindow returns a channel that is closed when the send window might have increased.
func (fc *FlowController) WaitSendWindow() <-chan struct{} {
	fc.mutex.Lock()
	defer fc.mutex.Unlock()
	return fc.wake
}

// AddReceivedBytes adds received bytes and checks if it's within the window.
func (fc *FlowController) AddReceivedBytes(n uint64) bool {
	fc.mutex.Lock()
	defer fc.mutex.Unlock()
	if fc.receivedBytes+n > fc.receiveWindow {
		return false
	}
	fc.receivedBytes += n
	return true
}

// AddReadBytes adds read bytes and returns whether a window update should be sent.
func (fc *FlowController) AddReadBytes(n uint64) (bool, uint64) {
	fc.mutex.Lock()
	defer fc.mutex.Unlock()
	fc.readBytes += n
	
	// Send window update if more than half of the window is consumed.
	if fc.receiveWindow - fc.readBytes <= fc.maxReceiveWindow / 2 {
		fc.receiveWindow = fc.readBytes + fc.maxReceiveWindow
		return true, fc.receiveWindow
	}
	return false, 0
}
