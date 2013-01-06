package main

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"flag"
	"fmt"
	"hash/adler32"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"sort"
	//"syscall"

	"github.com/cookieo9/resources-go/v2/resources"
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

// TODO: JVM startup time is expensive. Do something smarter and maybe parallelize
func java_deflater() (chan []byte, chan []byte) { //(data []byte) []byte {

	jar, err := resources.Open("java-deflate.jar")
	if err != nil {
		panic(err)
	}
	defer jar.Close()

	file_on_disk, err := ioutil.TempFile(".", "git-minecraft-smudge-jar")
	if err != nil {
		panic(err)
	}
	_, err = io.Copy(file_on_disk, jar)
	if err != nil {
		panic(err)
	}
	file_on_disk.Close()

	jarname := file_on_disk.Name()
	//log.Printf("Jarname = %s", jarname)

	c := exec.Command("java", "-jar", jarname)
	c.Stderr = os.Stderr

	stdin, err := c.StdinPipe()
	if err != nil {
		panic(err)
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		panic(err)
	}

	err = c.Start()
	if err != nil {
		panic(err)
	}

	in, out := make(chan []byte), make(chan []byte)

	//f, _ := stdout.(*os.File)

	//syscall.SetNonblock(int(f.Fd()), true)

	go func() {
		defer func() {
			log.Print("Defer is executing..")
			os.Remove(file_on_disk.Name())
			log.Print("Waiting..")
			err = c.Wait()
			log.Print("Waited..")
			if err != nil {
				panic(err)
			}
		}()

		for {
			input := <-in
			if len(input) == 0 {
				// We're done
				break
			}

			go func() {
				// Length
				err := binary.Write(stdin, binary.BigEndian, int32(len(input)))
				if err != nil {
					panic(err)
				}

				buf := bytes.NewBuffer(input)
				n, err := io.Copy(stdin, buf)
				if n != int64(len(input)) {
					log.Panicf("Amount written doesn't match expected size: %d != %d, err = %v",
						n, len(input), err)
				}
				if err != nil {
					panic(err)
				}
			}()

			var comprlen int32
			/*
				if done_one {
					x := make([]byte, 8)

					log.Panicf("x = %v", x)
				} else {
				}*/
			err = binary.Read(stdout, binary.BigEndian, &comprlen)
			if err != nil {
				log.Print("Err = ", err)
				panic(err)
			}

			if comprlen == 0 {
				log.Print("comprlen = 0!!!")
			}

			output := make([]byte, comprlen)
			n1, err := io.ReadFull(stdout, output)

			if n1 != int(comprlen) {
				log.Panicf("Amount read doesn't match expected size: %d != %d, err = %v",
					n1, comprlen, err)
			}
			if err != nil {
				log.Printf("!!! ERR = %v", err)
				panic(err)
			}
			out <- output
		}
		log.Print("Finishing compression..")
	}()

	return in, out
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
		panic("Read junk, stopping.")
		return
	}
	if compression_type != 2 {
		chunksize = 5
		log.Panicf("Unknown compression type %d", compression_type)
		return
	}
	chunksize -= 1

	//log.Printf("Reading header type %d size %d", compression_type, chunksize)

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
			panic("Empty sector?!")
			continue
		}

		datasize, format, data, err := read_chunk(input, size)

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
		/*
			if uint16((next - p)/4096) != sectorsize/4096 {
				log.Printf("p, sectorsize, datasize = %x, %x, %x", p, sectorsize, datasize)
				log.Printf("  delta = %d, %d", (next - p)/4096, sectorsize/4096)
			}
		*/
		_, _ = format, data

		junk := make([]byte, junksize)
		n, err := input.Read(junk)
		if n != int(junksize) || err != nil {
			panic(err)
		}

		//log.Printf("len(data) = %x", len(data))

		err = binary.Write(out, binary.BigEndian, uint32(len(data)))
		if err != nil {
			panic(err)
		}
		_, err = out.Write(data)
		if err != nil {
			panic(err)
		}
		//log.Printf("junksize = %x  / len(junk) = %x", junksize, len(junk))
		err = binary.Write(out, binary.BigEndian, junksize)
		if err != nil {
			panic(err)
		}
		_, err = out.Write(junk)
		if err != nil {
			panic(err)
		}

		/*
			var opos int64

			switch o := out.(type) {
			case io.WriteSeeker:
				opos, _ = o.Seek(0, 1)
			case *bytes.Buffer:
				opos = int64(o.Len())
			default:
				log.Panicf("unknown type %q", o)
			}
			log.Printf(" -- Position : %x", opos)
		*/
	}

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

	to_compress, compressed := java_deflater()

	defer func() {
		// Signal the deflater that we're done
		to_compress <- []byte{}
	}()

	var datalen uint32
	for {
		//i, _ := input.(io.ReadSeeker)
		//ipos, _ := i.Seek(0, 1)
		//log.Printf(" -- input position: %x", ipos)

		err := binary.Read(input, binary.BigEndian, &datalen)
		//log.Printf("Reading %x bytes", datalen)

		if err == io.EOF {
			break
		}
		if err != nil {
			panic(err)
		}

		if datalen > 1024*1024 {
			panic("Too big.")
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

		resmudgedbuf := &bytes.Buffer{}
		smudge(locations, tmpout, resmudgedbuf)
		h_after := GetHash(resmudgedbuf)
		if h_before != h_after {
			log.Printf("Smudged output doesn't equal input! %x != %x",
				h_before, h_after)
		}

		outbuf.WriteTo(out)

	default:
		log.Panicf("Unknown direction: %s", direction)
	}
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
	log.Print("Done")
}
