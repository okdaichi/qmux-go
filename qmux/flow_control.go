package qmux

import (
	"sync"
)

// flowController manages the flow control window.
type flowController struct {
	mutex sync.Mutex

	receiveWindow    uint64
	maxReceiveWindow uint64
	receivedBytes    uint64
	readBytes        uint64

	sendWindow uint64
	sentBytes  uint64

	// wake is closed when the send window is updated.
	wake chan struct{}
}

// newFlowController creates a new flow controller.
func newFlowController(initialWindow uint64) *flowController {
	return &flowController{
		receiveWindow:    initialWindow,
		maxReceiveWindow: initialWindow,
		sendWindow:       initialWindow,
		wake:             make(chan struct{}),
	}
}

// UpdateSendWindow updates the send window.
func (fc *flowController) UpdateSendWindow(limit uint64) {
	fc.mutex.Lock()
	defer fc.mutex.Unlock()
	if limit > fc.sendWindow {
		fc.sendWindow = limit
		close(fc.wake)
		fc.wake = make(chan struct{})
	}
}

// AddSentBytes adds sent bytes.
func (fc *flowController) AddSentBytes(n uint64) {
	fc.mutex.Lock()
	defer fc.mutex.Unlock()
	fc.sentBytes += n
}

// SendWindowRemaining returns the remaining send window.
func (fc *flowController) SendWindowRemaining() uint64 {
	fc.mutex.Lock()
	defer fc.mutex.Unlock()
	if fc.sentBytes >= fc.sendWindow {
		return 0
	}
	return fc.sendWindow - fc.sentBytes
}

// WaitSendWindow returns a channel that is closed when the send window is updated.
func (fc *flowController) WaitSendWindow() <-chan struct{} {
	fc.mutex.Lock()
	defer fc.mutex.Unlock()
	return fc.wake
}

// AddReceivedBytes adds received bytes and checks if it's within the window.
func (fc *flowController) AddReceivedBytes(n uint64) bool {
	fc.mutex.Lock()
	defer fc.mutex.Unlock()
	if fc.receivedBytes+n > fc.receiveWindow {
		return false
	}
	fc.receivedBytes += n
	return true
}

const windowUpdateThresholdDenominator = 2

// AddReadBytes adds read bytes and returns whether a window update should be sent.
func (fc *flowController) AddReadBytes(n uint64) (bool, uint64) {
	fc.mutex.Lock()
	defer fc.mutex.Unlock()
	fc.readBytes += n

	// Send window update if more than half of the window is consumed.
	if fc.receiveWindow-fc.readBytes <= fc.maxReceiveWindow/windowUpdateThresholdDenominator {
		fc.receiveWindow = fc.readBytes + fc.maxReceiveWindow
		return true, fc.receiveWindow
	}
	return false, 0
}
