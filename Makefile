.PHONY: all

all: git-minecraft-smudge git-minecraft-smudge-with-java

git-minecraft-smudge: main.go
	go get -v 
	go build

git-minecraft-smudge-with-java: git-minecraft-smudge java-deflate.jar
	zip data.zip java-deflate.jar
	cat git-minecraft-smudge data.zip > git-minecraft-smudge-with-java
	rm data.zip
	chmod u+x git-minecraft-smudge-with-java
	zip -A git-minecraft-smudge-with-java 

javadeflate/Cmd.class: javadeflate/Cmd.java
	javac $<
	
java-deflate.jar: javadeflate/Cmd.class
	zip $@ $< META-INF/MANIFEST.MF
	
