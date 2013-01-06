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

func clean(locations Locations, input io.Reader, out io.Writer) {
	next := uint32(0x2000)

	for i, loc := range locations {

		// TODO: Write test case for circumstances that 0x2000 isn't in the
		//		 locations table. (e.g, manually fudge a file)

		p, size := loc.Decode()
		if size == 0 {
			// We don't care about empty sectors, they just haven't been loaded
			// yet...
			continue
		}

		datasize, _, data, err := read_chunk(input, size)

		if err != nil {
			log.Print("Failed to read chunk %d", i)
		}

		junksize := uint32(size) - datasize

		if i < len(locations)-1 {
			next, _ = locations[i+1].Decode()
			this_size := next - p
			// Take into account holes in the file
			junksize += this_size - uint32(size)
		}

		junk := make([]byte, junksize)
		n, err := input.Read(junk)
		if n != int(junksize) || err != nil {
			panic(err)
		}

		err = binary.Write(out, binary.BigEndian, uint32(len(data)))
		if err != nil {
			panic(err)
		}
		_, err = out.Write(data)
		if err != nil {
			panic(err)
		}

		err = binary.Write(out, binary.BigEndian, junksize)
		if err != nil {
			panic(err)
		}
		_, err = out.Write(junk)
		if err != nil {
			panic(err)
		}
	}

	// Sentinel
	err := binary.Write(out, binary.BigEndian, uint32(0))
	if err != nil {
		panic(err)
	}

	// TODO: Copy whatever remains of the file
	// Note: READER must also do something sane with these bytes
	n, err := io.Copy(out, input)
	if err != nil {
		log.Panic(err)
	}
	log.Printf("Copied %d bytes at EOF", n)

}

func smudge(locations Locations, input io.Reader, out io.Writer) {
	/*
		defer func() {
			if err := recover(); err != nil {
				i, _ := input.(io.ReadSeeker)
				o, _ := out.(io.WriteSeeker)
				ipos, _ := i.Seek(0, 1)
				opos, _ := o.Seek(0, 1)
				log.Printf("Position at panic: %x / %x", ipos, opos)
				panic(err)
			}
		}()
	*/

	buf := &bytes.Buffer{}
	_, err := io.Copy(buf, input)
	//log.Printf("Copying %d bytes (Err = %v)", n, err)
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
		err := binary.Read(input, binary.BigEndian, &datalen)

		if err == io.EOF {
			log.Print("Hit EOF")
			break
		}
		if err != nil {
			panic(err)
		}

		if datalen > 1024*1024 {
			panic("Too big.")
		}
		if datalen == 0 {
			//panic("datalen == 0")
			// Copy the rest of what remains
			_, err := io.Copy(out, input)
			if err != nil {
				panic(err)
			}
			break
		}

		data := make([]byte, datalen)
		_, err = input.Read(data)
		if err != nil {
			panic(err)
		}

		to_compress <- data
		deflated := <-compressed

		// Chunk size
		err = binary.Write(out, binary.BigEndian, uint32(len(deflated)+1))
		if err != nil {
			panic(err)
		}

		// Compression type
		err = binary.Write(out, binary.BigEndian, uint8(2))
		if err != nil {
			panic(err)
		}

		_, err = out.Write(deflated)
		if err != nil {
			panic(err)
		}

		err = binary.Read(input, binary.BigEndian, &datalen)

		if err != nil {
			panic(err)
		}
		//log.Printf("Datalen = %x", datalen)
		data = make([]byte, datalen)
		_, err = input.Read(data)
		if err != nil {
			panic(err)
		}

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
	var err error
	var locations Locations
	var timestamps [1024]uint32

	err = binary.Read(input, binary.BigEndian, &locations)
	if err != nil {
		panic(err)
	}
	binary.Write(out, binary.BigEndian, locations)
	if err != nil {
		panic(err)
	}

	err = binary.Read(input, binary.BigEndian, &timestamps)
	if err != nil {
		panic(err)
	}
	binary.Write(out, binary.BigEndian, timestamps)
	if err != nil {
		panic(err)
	}

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
