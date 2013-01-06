package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"flag"
	"fmt"
	"hash/adler32"
	"io"
	"log"
	"os"
	"sort"
)

type Location uint32

const JUNK_MAGIC = 0xBEEFDEAD

func (l Location) Pos() uint32 { pos, _ := l.Decode(); return pos }
func (l Location) Decode() (pos uint32, size uint16) {
	pos = uint32((l & 0xFFFF00) * 0x10)
	size = uint16((l & 0xFF) * 0x1000)
	return
}
func (l Location) String() string {
	p, s := l.Decode()
	return fmt.Sprintf("{%x, %x}", p, s)
}

type Locations [1024]Location

func (s *Locations) Len() int           { return len(s) }
func (s *Locations) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }
func (s *Locations) Less(i, j int) bool { return s[i].Pos() < s[j].Pos() }

func read_chunk(input io.Reader, sectorsize uint16) (chunksize uint32, compression_type uint8, data []byte, err error) {

	err = binary.Read(input, binary.BigEndian, &chunksize)
	if err != nil {
		log.Panic(err)
	}

	err = binary.Read(input, binary.BigEndian, &compression_type)
	if err != nil {
		log.Panic(err)
	}
	if chunksize > uint32(sectorsize) {
		log.Printf("Chunk size > sector size: %x > %x", chunksize, sectorsize)
		chunksize = 5
		panic("Read junk, stopping.")
		return
	}
	if compression_type != 2 {
		chunksize = 5
		log.Panicf("Unknown compression type %d", compression_type)
		return
	}
	chunksize -= 1

	limited_in := io.LimitReader(input, int64(chunksize))
	compressed_data := &bytes.Buffer{}
	compressed_in := io.TeeReader(limited_in, compressed_data)

	in, err := zlib.NewReader(compressed_in)
	if err != nil {
		log.Panic(err)
	}

	decompressed_data := &bytes.Buffer{}
	_, err = io.Copy(decompressed_data, in)

	lin, _ := limited_in.(*io.LimitedReader)
	if lin.N != 0 {
		log.Panicf("Still got %d bytes left", lin.N)
	}
	if err != nil {
		log.Panic(err)
	}
	in.Close()

	data = decompressed_data.Bytes()

	// sizeof(size) + sizeof(format)
	chunksize += 5
	return
}

func clean(locations Locations, input io.Reader, out io.Writer) {
	var pos uint32 = 0x2000

	for i, loc := range locations {

		// TODO: Write test case for circumstances that 0x2000 isn't in the
		//		 locations table. (e.g, manually fudge a file)

		p, size := loc.Decode()

		if pos < p {
			checked_bin_write(out, uint32(JUNK_MAGIC))

			junklen := p - pos
			data := checked_byteslice_read(input, uint64(junklen))
			pos += junklen

			checked_bin_write(out, junklen)

			_, err := out.Write(data)
			if err != nil {
				panic(err)
			}
		}

		if size == 0 {
			// We don't care about empty locations, they just haven't been loaded
			// yet...
			continue
		}

		datasize, _, data, err := read_chunk(input, size)
		pos += uint32(size)

		if err != nil {
			log.Print("Failed to read chunk %d", i)
		}

		junksize := uint32(size) - datasize

		if i < len(locations)-1 {
			next, _ := locations[i+1].Decode()
			this_size := next - p
			// Take into account holes in the file
			junksize += this_size - uint32(size)
		}

		junk := make([]byte, junksize)
		_, err = io.ReadFull(input, junk)
		pos += junksize
		if err != nil {
			panic(err)
		}

		checked_bin_write(out, uint32(len(data)))

		_, err = out.Write(data)
		if err != nil {
			panic(err)
		}

		checked_bin_write(out, junksize)

		_, err = out.Write(junk)
		if err != nil {
			panic(err)
		}
	}

	// Sentinel
	checked_bin_write(out, uint32(0))

	// TODO: Copy whatever remains of the file
	// Note: READER must also do something sane with these bytes
	_, err := io.Copy(out, input)
	if err != nil {
		log.Panic(err)
	}
	//log.Printf("Copied %d bytes at EOF", n)

}

func checked_bin_read(in io.Reader, data interface{}) {
	err := binary.Read(in, binary.BigEndian, data)
	if err != nil {
		panic(err)
	}
}

func checked_bin_write(out io.Writer, data interface{}) {
	err := binary.Write(out, binary.BigEndian, data)
	if err != nil {
		panic(err)
	}
}

func checked_byteslice_read(in io.Reader, len uint64) []byte {
	data := make([]byte, len)
	n, err := io.ReadFull(in, data)
	if uint64(n) != len {
		log.Panicf("checked_byteslice_read failed expected %d got %d err = %v",
			len, n, err)
	}
	if err != nil {
		panic(err)
	}
	return data
}

func smudge(locations Locations, input io.Reader, out io.Writer) {
	buf := &bytes.Buffer{}
	_, err := io.Copy(buf, input)
	if err != nil {
		panic(err)
	}
	input = buf

	to_compress, compressed, cleanup := java_deflater()

	defer func() {
		// Signal the deflater that we're done
		to_compress <- []byte{}
		cleanup()
	}()

	var datalen uint32
	for {
		// Read uncompressed sector length
		err := binary.Read(input, binary.BigEndian, &datalen)

		if err == io.EOF {
			log.Print("Hit EOF")
			break
		}
		if err != nil {
			panic(err)
		}

		if datalen == JUNK_MAGIC {
			// If it's the junk magic then we have some junk that needs reading
			// first.

			checked_bin_read(input, &datalen) // Read Junk Size

			// Read Junk
			//junk := checked_byteslice_read(input, uint64(datalen))
			n, err := io.CopyN(out, input, int64(datalen))
			if n != int64(datalen) {
				log.Panicf("Bad CopyN expected %d got %d err = %v", datalen, n, err)
			}
			if err != nil {
				panic(err)
			}

			checked_bin_read(input, &datalen) // Read new subsequent datalen

		} else if datalen > 1024*1024 {
			panic("Too big.")
		} else if datalen == 0 {
			//panic("datalen == 0")
			// Copy the rest of what remains
			_, err := io.Copy(out, input)
			if err != nil {
				panic(err)
			}
			break
		}

		// Read uncompressed sector
		data := checked_byteslice_read(input, uint64(datalen))

		// Get java to recompress it
		to_compress <- data
		deflated := <-compressed

		// Write compressed chunk size
		checked_bin_write(out, uint32(len(deflated)+1))
		// Write compression type
		checked_bin_write(out, uint8(2))
		// Read junk length
		checked_bin_read(input, &datalen)
		// Read junk
		data = checked_byteslice_read(input, uint64(datalen))

		// Write compressed data
		_, err = out.Write(deflated)
		if err != nil {
			panic(err)
		}

		// Emit junk
		_, err = out.Write(data)
		if err != nil {
			panic(err)
		}
	}
}

// Obtain a hash of buffer without modifying its contents
func GetHash(buf *bytes.Buffer) uint32 {
	h := adler32.New()
	// Use a new buffer so we don't modify the old one by reading it
	bytes.NewBuffer(buf.Bytes()).WriteTo(h)
	return h.Sum32()
}

func process_stream(direction string, input io.Reader, out io.Writer) {
	var locations Locations
	var timestamps [1024]uint32

	checked_bin_read(input, &locations)
	checked_bin_read(input, &timestamps)

	checked_bin_write(out, locations)
	checked_bin_write(out, timestamps)

	sort.Sort(&locations)

	switch direction {
	case "smudge":
		smudge(locations, input, out)
	case "clean":
		inputbuf := &bytes.Buffer{}

		_, err := io.Copy(inputbuf, input)
		if err != nil {
			panic(err)
		}
		h_before := GetHash(inputbuf)

		outbuf := &bytes.Buffer{}
		tmpout := &bytes.Buffer{}

		outbufs := io.MultiWriter(outbuf, tmpout)
		clean(locations, inputbuf, outbufs)

		// The following can be removed if this code is ever trusted
		resmudgedbuf := &bytes.Buffer{}
		smudge(locations, tmpout, resmudgedbuf)
		h_after := GetHash(resmudgedbuf)
		if h_before != h_after {
			log.Printf("Smudged output doesn't equal input! %x != %x",
				h_before, h_after)
		}
		_ = h_before

		outbuf.WriteTo(out)

	default:
		log.Panicf("Unknown direction: %s", direction)
	}
}

func process_file(direction string, filename string) {
	var input io.Reader
	var out io.Writer
	var err error

	//log.Print("Processing ", filename)

	if filename == "-" {
		input = os.Stdin
		out = os.Stdout
	} else {
		input, err = os.Open(filename)
		if err != nil {
			log.Panic(err)
		}
		switch direction {
		case "smudge":
			out, err = os.Create(filename + ".git.smudged")
		case "clean":
			out, err = os.Create(filename + ".git.cleaned")
		}
		if err != nil {
			log.Panic(err)
		}
	}

	process_stream(direction, input, out)
}

func main() {
	//log.Print("Begin")
	flag.Parse()

	if flag.NArg() < 1 {
		log.Fatalf("usage: %s [smudge|clean] [file...]", "git-minecraft-smudge")
	}

	direction := flag.Args()[0]

	for _, filename := range flag.Args()[1:] {
		process_file(direction, filename)
	}
	log.Print("Done")
}
