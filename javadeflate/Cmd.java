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
        DataInputStream in = new DataInputStream(System.in);
        DataOutputStream out = new DataOutputStream(System.out);


		try {
			// Read (len, bytes...) then write (len, compressedbytes...)
			while (true) {
				int amount = in.readInt();
				if (amount == 0) {
					break;
				}

				byte[] data = new byte[amount];
				int result = in.read(data, 0, data.length);

				byte[] compressed = new byte[amount];

        		Deflater deflater = new Deflater();
				deflater.setInput(data);
				deflater.finish();

				int len = deflater.deflate(compressed);

				out.writeInt(len);
				out.write(compressed, 0, len);
	        }
	    } catch (EOFException e) {
	    	
		} catch (IOException e) {
			System.err.println("Failure to write..");
			return;
		}        
        
        try {
		    if(in != null) in.close();
	    } catch (IOException e) {}
	    
	    //System.err.println("Success.");
    }
}
