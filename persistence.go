// Package persistence allows doozer to save state across server restarts.
// Doozer mutations are apended to the file on disk.  Deletions are
// garbage collected from the head of the log file so it does not
// grow indefinitely.

package persistence

import (
	"encoding/binary"
	"errors"
	"io"
)

// Journal represents a file where doozer can save state.
type Journal struct {
	r chan *iop // reads are issued here, pointer b.c. user modifies it.
	w chan iop  // writes are issued here.
	q chan bool // quit signal.
}

// iop represents an I/O request.  err is used to signal the result back
// to the client.  mut is filled by the client, in case of writes and
// by the system in case of reads.
type iop struct {
	mut string
	err chan error
}

// NewJournal opens the named file for synchronous I/O, creating it
// with mode 0640 if it does not exist and prepares it for logging operation.
// If successful, methods on the returned Journal can be used for I/O.
// It returns a Journal and an error, if any.
func NewJournal(name string) (j *Journal, err error) {
	j.r = make(chan *iop)
	j.w = make(chan iop)
	j.q = make(chan bool)
	arena, err := newArena(name)
	if err != nil {
		return nil, err
	}
	go iops(arena, j)
	return
}

// Store writes the mutation to the Journal.
func (j *Journal) Store(mutation string) error {
	wrq := iop{mutation, make(chan error)}
	j.w <- wrq
	err := <-wrq.err

	return err
}

// Retrieve reads the next mutation from the Journal.  It returns
// the mutation and an error, if any.  EOF is signaled by a nil
// mutation with err set to io.EOF
func (j *Journal) Retrieve() (mutation string, err error) {
	rrq := iop{err: make(chan error)}
	j.r <- &rrq
	err = <-rrq.err

	return rrq.mut, err
}

// iops sits in a loop and processes requests sent by Store and Retrieve.
// Clients of this function specify a channel where it can send back
// the result of the operation. 
func iops(rw io.ReadWriteCloser, j *Journal) {
	defer rw.Close()
	for {
		select {
		case rop := <-j.r:
			mut, err := decodedRead(rw)
			rop.mut = mut
			rop.err <- err

		case wop := <-j.w:
			wop.err <- encodedWrite(rw, wop.mut)

		case <-j.q:
			return
		}
	}
	return
}

// decodedRead reads a block from the reader, decodes it into a
// mutation and returns it along with an error.
func decodedRead(r io.Reader) (mut string, err error) {
	b := block{}

	// Read the header so we know how much to read next.
	err = binary.Read(r, binary.LittleEndian, &b.hdr)
	if err != nil {
		return
	}

	// If everything went fine, we can read the data.
	_, err = io.ReadAtLeast(r, b.data, b.hdr.size)

	// We need to make sure the checksum is valid.
	if !b.isValid() {
		err = errors.New("read an invalid block")
		return
	}

	mut = b.String()
	return
}

// encodedRead encodes a mutation into a block and writes the
// block to the writer returning an error.
func encodedWrite(w io.Writer, mut string) (err error) {
	b := newBlock(mut)

	// We use two write calls bacause we use encoding/binary
	// to write the fixed length header.

	err = binary.Write(w, binary.LittleEndian, b.hdr)
	if err != nil {
		return
	}

	// We'we written the header successfully, write the rest
	// of the data.
	_, err = w.Write(b.data)
	return
}
