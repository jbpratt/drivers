package hd44780

import (
	"errors"
	"image/color"
	"io"
	"time"
)

type Buser interface {
	io.ReadWriter
	SetCommandMode(set bool)
}

// NewGPIO4Bit returns 4bit data length HD44780 driver. Datapins are LCD DB pins starting from DB4 to DB7
func NewGPIO4Bit(dataPins []uint8, e, rs, rw uint8) (Device, error) {
	const fourBitMode = 4
	if len(dataPins) != fourBitMode {
		return Device{}, errors.New("4 pins are required in data slice (D7-D4) when HD44780 is used in 4 bit mode")
	}
	return newGPIO(dataPins, e, rs, rw, DATA_LENGTH_4BIT), nil
}

// NewGPIO8Bit returns 8bit data length HD44780 driver. Datapins are LCD DB pins starting from DB0 to DB7
func NewGPIO8Bit(dataPins []uint8, e, rs, rw uint8) (Device, error) {
	const eightBitMode = 8
	if len(dataPins) != eightBitMode {
		return Device{}, errors.New("8 pins are required in data slice (D7-D0) when HD44780 is used in 8 bit mode")
	}
	return newGPIO(dataPins, e, rs, rw, DATA_LENGTH_8BIT), nil
}

type Device struct {
	bus          Buser
	width        uint8
	height       uint8
	buffer       []uint8
	bufferLength uint8

	rowOffset  []uint8 // Row offsets in DDRAM
	datalength uint8

	cursor     cursor
	busyStatus []byte
}

type cursor struct {
	x, y uint8
}

type Config struct {
	Width       int16
	Height      int16
	CursorBlink bool
	CursorOnOff bool
	Font        uint8
}

// Configure initializes device
func (d *Device) Configure(cfg Config) error {
	d.busyStatus = make([]byte, 1)
	d.width = uint8(cfg.Width)
	d.height = uint8(cfg.Height)
	if d.width == 0 || d.height == 0 {
		return errors.New("Width and height must be set")
	}
	memoryMap := uint8(ONE_LINE)
	if d.height > 1 {
		memoryMap = TWO_LINE
	}
	d.setRowOffsets()
	d.ClearBuffer()

	cursor := CURSOR_OFF
	if cfg.CursorOnOff {
		cursor = CURSOR_ON
	}
	cursorBlink := CURSOR_BLINK_OFF
	if cfg.CursorBlink {
		cursorBlink = CURSOR_BLINK_ON
	}
	if !(cfg.Font == FONT_5X8 || cfg.Font == FONT_5X10) {
		cfg.Font = FONT_5X8
	}

	//Wait 15ms after Vcc rises to 4.5V
	time.Sleep(15 * time.Millisecond)

	d.bus.SetCommandMode(true)
	d.bus.Write([]byte{DATA_LENGTH_8BIT})
	time.Sleep(5 * time.Millisecond)

	for i := 0; i < 2; i++ {
		d.bus.Write([]byte{DATA_LENGTH_8BIT})
		time.Sleep(150 * time.Microsecond)

	}

	if d.datalength == DATA_LENGTH_4BIT {
		d.bus.Write([]byte{DATA_LENGTH_4BIT >> 4})
	}

	// Busy flag is now accessible
	d.SendCommand(memoryMap | cfg.Font | d.datalength)
	d.SendCommand(DISPLAY_OFF)
	d.SendCommand(DISPLAY_CLEAR)
	d.SendCommand(ENTRY_MODE | CURSOR_INCREASE | DISPLAY_NO_SHIFT)
	d.SendCommand(DISPLAY_ON | uint8(cursor) | uint8(cursorBlink))
	return nil
}

// Write writes data to internal buffer
func (d *Device) Write(data []byte) (n int, err error) {
	size := len(data)
	if size > len(d.buffer) {
		size = len(d.buffer)
	}
	d.bufferLength = uint8(size)
	for i := uint8(0); i < d.bufferLength; i++ {
		d.buffer[i] = data[i]
	}
	return size, nil
}

// Display sends the whole buffer to the screen at cursor position
func (d *Device) Display() error {

	// Buffer may contain less characters than its capacity.
	// We must be sure that we will not send unassigned characters
	// That would result in sending zero values of buffer slice and
	// potentialy displaying some character.
	var totalDisplayedChars uint8

	var bufferPos uint8

	for ; d.cursor.y < d.height; d.cursor.y++ {
		d.SetCursor(d.cursor.x, d.cursor.y)

		for ; d.cursor.x < d.width && totalDisplayedChars < d.bufferLength; d.cursor.x++ {
			d.sendData(d.buffer[bufferPos])
			bufferPos++
			totalDisplayedChars++
		}
		if d.cursor.x >= d.width {
			d.cursor.x = 0
		}
		if totalDisplayedChars >= d.bufferLength {
			break
		}

	}
	return nil
}

// SetCursor moves cursor to position x,y, where (0,0) is top left corner and (width-1, height-1) bottom right
func (d *Device) SetCursor(x, y uint8) {
	d.cursor.x = x
	d.cursor.y = y
	d.SendCommand(DDRAM_SET | (x + (d.rowOffset[y] * y)))
}

// SetRowOffsets sets initial memory addresses coresponding to the display rows
// Each row on display has different starting address in DDRAM. Rows are not mapped in order.
// These addresses tend to differ between the types of the displays (16x2, 16x4, 20x4 etc ..),
// https://web.archive.org/web/20111122175541/http://web.alfredstate.edu/weimandn/lcd/lcd_addressing/lcd_addressing_index.html
func (d *Device) setRowOffsets() {
	switch d.height {
	case 1:
		d.rowOffset = []uint8{}
	case 2:
		d.rowOffset = []uint8{0x0, 0x40, 0x0, 0x40}
	case 4:
		d.rowOffset = []uint8{0x0, 0x40, d.width, 0x40 + d.width}
	default:
		d.rowOffset = []uint8{0x0, 0x40, d.width, 0x40 + d.width}

	}
}

// SendCommand sends commands to driver
func (d *Device) SendCommand(command byte) {
	d.bus.SetCommandMode(true)
	d.bus.Write([]byte{command})

	for d.Busy() {
	}
}

// sendData sends byte data directly to display.
func (d *Device) sendData(data byte) {
	d.bus.SetCommandMode(false)
	d.bus.Write([]byte{data})

	for d.Busy() {
	}
}

// CreateCharacter crates characters using data and stores it under cgram Addr in CGRAM
func (d *Device) CreateCharacter(cgramAddr uint8, data []byte) {
	d.SendCommand(CGRAM_SET | cgramAddr)
	for _, dd := range data {
		d.sendData(dd)
	}
}

// Busy returns true when hd447890 is busy
func (d *Device) Busy() bool {
	d.bus.Read(d.busyStatus)
	return (d.busyStatus[0] & BUSY) > 0
}

// SetPixel is not supported on devices which uses HD44780 driver
func (d *Device) SetPixel(x, y int16, c color.RGBA) {
	panic("HD44780 does not support setting individual pixels")
}

// Size returns the current size of the display.
func (d *Device) Size() (w, h int16) {
	return int16(d.width), int16(d.height)
}

// ClearDisplay clears displayed content and buffer
func (d *Device) ClearDisplay() {
	d.SendCommand(DISPLAY_CLEAR)
	d.ClearBuffer()
}

// ClearBuffer clears internal buffer
func (d *Device) ClearBuffer() {
	d.buffer = make([]uint8, d.width*d.height)
}
