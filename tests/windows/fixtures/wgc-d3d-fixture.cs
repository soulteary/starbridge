// Generated-resource test only. Reflection exercises the production ABI without
// adding a test entry point or software-device option to the capture API.
using System;
using System.Diagnostics;
using System.Reflection;
using System.Runtime.InteropServices;
using System.Threading;
public static class SyntheticWgcD3D {
    const BindingFlags PrivateStatic=BindingFlags.NonPublic|BindingFlags.Static;
    static Delegate Slot(Type adapter,IntPtr instance,int slot,string name) {
        Type signature=adapter.GetNestedType(name,BindingFlags.NonPublic);
        return (Delegate)adapter.GetMethod("Method",PrivateStatic).MakeGenericMethod(signature).Invoke(null,new object[]{instance,slot});
    }
    static void Check(int result) {if(result<0)throw new InvalidOperationException("Generated D3D fixture operation failed.");}
    static object Description(Type adapter,uint usage,uint cpu) {
        Type type=adapter.GetNestedType("TextureDescription",BindingFlags.NonPublic);object result=Activator.CreateInstance(type);
        foreach(string name in new string[]{"width","height","mips","array","samples"})type.GetField(name).SetValue(result,(uint)(name=="width"||name=="height"?2:1));
        type.GetField("format").SetValue(result,(uint)87);type.GetField("usage").SetValue(result,usage);type.GetField("cpu").SetValue(result,cpu);return result;
    }
    static void Release(IntPtr value) {if(value!=IntPtr.Zero)Marshal.Release(value);}
    public static byte[] Run(Type adapter) {
        IntPtr device=IntPtr.Zero,context=IntPtr.Zero,source=IntPtr.Zero,staging=IntPtr.Zero,data=IntPtr.Zero,initial=IntPtr.Zero;bool mapped=false;
        try {
            // D3D_DRIVER_TYPE_WARP exercises actual D3D11 without requiring a GPU.
            object[] create={IntPtr.Zero,(uint)5,IntPtr.Zero,(uint)0x20,IntPtr.Zero,(uint)0,(uint)7,IntPtr.Zero,(uint)0,IntPtr.Zero};
            Check((int)adapter.GetMethod("D3D11CreateDevice",PrivateStatic).Invoke(null,create));device=(IntPtr)create[7];context=(IntPtr)create[9];
            byte[] pixels={0,0,255,255,0,255,0,255,255,0,0,255,255,255,255,255};
            data=Marshal.AllocHGlobal(pixels.Length);Marshal.Copy(pixels,0,data,pixels.Length);
            initial=Marshal.AllocHGlobal(IntPtr.Size+8);Marshal.WriteIntPtr(initial,data);Marshal.WriteInt32(initial,IntPtr.Size,8);Marshal.WriteInt32(initial,IntPtr.Size+4,16);
            Delegate texture=Slot(adapter,device,5,"CreateTexture");
            object[] sourceArguments={device,Description(adapter,0,0),initial,IntPtr.Zero};Check((int)texture.DynamicInvoke(sourceArguments));source=(IntPtr)sourceArguments[3];
            object[] descriptionArguments={source,Description(adapter,0,0)};Slot(adapter,source,10,"GetDescription").DynamicInvoke(descriptionArguments);
            object description=descriptionArguments[1];Type descriptionType=description.GetType();
            if((uint)descriptionType.GetField("width").GetValue(description)!=2 || (uint)descriptionType.GetField("height").GetValue(description)!=2 || (uint)descriptionType.GetField("format").GetValue(description)!=87)throw new InvalidOperationException("Generated D3D texture description changed.");
            object[] stagingArguments={device,Description(adapter,3,0x20000),IntPtr.Zero,IntPtr.Zero};Check((int)texture.DynamicInvoke(stagingArguments));staging=(IntPtr)stagingArguments[3];
            Slot(adapter,context,47,"CopyResource").DynamicInvoke(new object[]{context,staging,source});Slot(adapter,context,111,"FlushContext").DynamicInvoke(new object[]{context});
            Type mappedType=adapter.GetNestedType("Mapped",BindingFlags.NonPublic);object[] map={context,staging,(uint)0,(uint)1,(uint)0x100000,Activator.CreateInstance(mappedType)};
            Stopwatch clock=Stopwatch.StartNew();Delegate mapResource=Slot(adapter,context,14,"MapResource");
            while(true){int hr=(int)mapResource.DynamicInvoke(map);if(hr==unchecked((int)0x887A000A)){if(clock.ElapsedMilliseconds>5000)throw new InvalidOperationException("Generated D3D readback exceeded its deadline.");Thread.Sleep(5);continue;}Check(hr);mapped=true;break;}
            object buffer=map[5];IntPtr pointer=(IntPtr)mappedType.GetField("data").GetValue(buffer);uint pitch=(uint)mappedType.GetField("rowPitch").GetValue(buffer);
            if(pitch>Int32.MaxValue)throw new InvalidOperationException("Generated D3D pitch is invalid.");
            return (byte[])adapter.GetMethod("EncodeBgra").Invoke(null,new object[]{pointer,(int)pitch,2,2,4,4096});
        } finally {
            if(mapped)Slot(adapter,context,15,"UnmapResource").DynamicInvoke(new object[]{context,staging,(uint)0});
            Release(staging);Release(source);Release(context);Release(device);if(initial!=IntPtr.Zero)Marshal.FreeHGlobal(initial);if(data!=IntPtr.Zero)Marshal.FreeHGlobal(data);
        }
    }
}
