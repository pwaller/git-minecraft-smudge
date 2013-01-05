package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
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

func determine_compression_level(uncompr, compr []byte) int {
	var compressed_buffer bytes.Buffer

	fd, err := os.Create(fmt.Sprintf("orig.bin"))
	if err != nil {
		panic(err)
	}
	fd.Write(compr)
	fd.Close()

	fd, err = os.Create(fmt.Sprintf("java-deflated.bin"))
	if err != nil {
		panic(err)
	}
	fd.Write(java_deflate(uncompr))
	fd.Close()

	fd, err = os.Create(fmt.Sprintf("uncompressed.bin"))
	if err != nil {
		panic(err)
	}
	fd.Write(uncompr)
	fd.Close()

	for i := 0; i < 10; i++ {

		compressed_buffer.Reset()

		var err error
		w, err := zlib.NewWriterLevel(&compressed_buffer, i)
		if err != nil {
			panic(err)
		}
		n, err := w.Write(uncompr)
		if n != len(uncompr) || err != nil {
			log.Panicf("Short write: %d, expected %d (buf = %v) err = %v", n, len(uncompr), compressed_buffer.Len(), err)
		}
		w.Close()

		fd, err := os.Create(fmt.Sprintf("compr.%d.bin", i))
		if err != nil {
			panic(err)
		}
		fd.Write(compressed_buffer.Bytes())
		fd.Close()

		log.Printf("Tried level %d got - %d - %d / %d", i, len(uncompr), compressed_buffer.Len(), len(compr))

		if bytes.Equal(compr, compressed_buffer.Bytes()) {
			return i
		}
	}
	return -1
}

func java_deflate(data []byte) []byte {
	c := exec.Command("java", "-jar", "java-deflate.jar")
	go c.Run()
	stdin, err := c.StdinPipe()
	if err != nil {
		panic(err)
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		panic(err)
	}
	n, err := stdin.Write(data)
	if n != len(data) || err != nil {
		panic(err)
	}
	stdin.Close()
	result, err := ioutil.ReadAll(stdout)
	if err != nil {
		panic(err)
	}
	return result
}

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
		return
	}
	if compression_type != 2 {
		chunksize = 5
		return
	}
	chunksize -= 1

	if compression_type == 72 {
		// read the true size
	}

	log.Printf("Reading header type %d size %d", compression_type, chunksize)

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

	if filename == "-" {
		input = os.Stdin
		out = os.Stdout
	} else {
		input, err = os.Open(filename)
		if err != nil {
			log.Panic(err)
		}
		out, err = os.Create(filename + ".git.smudged")
		if err != nil {
			log.Panic(err)
		}
	}

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

	log.Print(locations[:10])

	//nextpos = locations[0]

	//curpos := 0x2000
	for i, loc := range locations {
		log.Printf("Got %d locs left timestamp = %x", len(locations)-i, timestamps[i])
		p, sectorsize := loc.Decode()
		if sectorsize == 0 {
			continue
		}

		datasize, format, data, err := read_chunk(input, sectorsize)

		log.Printf("p, sectorsize, datasize = %x, %x, %x", p, sectorsize, datasize)

		if err != nil {
			log.Print("Failed to read chunk %d", i)
		}

		junk := uint32(sectorsize) - datasize
		//junk := int(nextloc) - ()
		_, _ = format, data

		//log.Printf("There are 0x%x junk bytes", junk)

		j := make([]byte, junk)
		n, err := input.Read(j)
		if n != int(junk) || err != nil {
			panic(err)
		}

		out.Write(data)
		//out.Write(j)

		//sk, _ := input.(io.Seeker)
		//pos, _ := sk.Seek(0, 1)
		//log.Printf("Current input position: %d (%x)", pos, pos)

	}

	_ = out
}

func main() {
	log.Print("Begin")
	flag.Parse()

	if flag.NArg() < 1 {
		log.Fatalf("usage: %s [smudge|clean] [file...]", "git-minecraft-smudge")
	}

	direction := flag.Args()[0]

	for _, filename := range flag.Args()[1:] {
		process_file(direction, filename)
	}
}
