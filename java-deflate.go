package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"sync"

	"github.com/cookieo9/resources-go/v2/resources"
)

func run_jar() (io.Writer, io.Reader, func()) {

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

	return stdin, stdout, func() {
		os.Remove(file_on_disk.Name())
	}
}

func java_deflater() (chan []byte, chan []byte, func()) {

	in, out := make(chan []byte), make(chan []byte)

	var stdin io.Writer = nil
	var stdout io.Reader = nil
	var cleanup func() = func() {}

	var once sync.Once

	go func() {
		for {
			input := <-in
			if len(input) == 0 {
				if stdin != nil {
					binary.Write(stdin, binary.BigEndian, int32(0))
				}
				break
			}
			once.Do(func() {
				// Only initialize java if we actually need it
				stdin, stdout, cleanup = run_jar()
			})

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

			err := binary.Read(stdout, binary.BigEndian, &comprlen)
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
		//log.Print("Finishing compression..")
	}()
	return in, out, func() {
	    cleanup()
	}
}
