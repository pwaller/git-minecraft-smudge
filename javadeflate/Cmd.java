// Decompiled by Jad v1.5.8e. Copyright 2001 Pavel Kouznetsov.
// Jad home page: http://www.geocities.com/kpdus/jad.html
// Decompiler options: packimports(3) 
// Source File Name:   JavaDeflateCmd.java

package javadeflate;

import java.io.*;
import java.util.zip.Deflater;
import java.util.zip.DeflaterOutputStream;

public class Cmd
{
    public static void main(String args[])
    {
        InputStream inputstream = null;
        PrintStream printstream = null;
        DataInputStream datainputstream = null;
        
        System.err.println("Deflate start.");
        
        inputstream = System.in;
        datainputstream = new DataInputStream(inputstream);
        
        Deflater deflater = null;
        DeflaterOutputStream deflateroutputstream = null;
        
        deflater = new Deflater();
        deflateroutputstream = new DeflaterOutputStream(System.out, deflater);

		int nRead;
		byte[] data = new byte[10*1024*1024];

		try {
			System.err.println("Reading in and writing out..");
			while ((nRead = datainputstream.read(data, 0, data.length)) != -1) {
			  //buffer.write(data, 0, nRead);
			  System.err.printf("Writing .. %d\n", nRead);
			  deflateroutputstream.write(data, 0, nRead);
			}
			deflateroutputstream.flush();
        	deflateroutputstream.finish();
		} catch (IOException e) {
			System.err.println("Failure to write..");
			return;
		}
        
        
        try {
		    if(deflateroutputstream != null) deflateroutputstream.close();            
		    if(datainputstream != null) datainputstream.close();
		    if(printstream != null) printstream.close();
		    if(inputstream != null) inputstream.close();
	    } catch (IOException e) {}
	    System.err.println("Success.");
    }
}
