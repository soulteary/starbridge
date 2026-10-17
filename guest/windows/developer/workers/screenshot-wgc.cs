// ABI declarations follow Microsoft's Windows SDK headers. This source is
// compiled with the existing .NET Framework compiler, without SDK projections.
namespace StarbridgeDeveloperScreenshot {
    using System;
    using System.Diagnostics;
    using System.Drawing;
    using System.Drawing.Imaging;
    using System.Runtime.InteropServices;
    using System.Threading;

    public sealed class CaptureRect { public int x,y,width,height; }
    public sealed class WgcProbe { public int os_build; public bool available; public string reason; }
    public static class Wgc {
        [StructLayout(LayoutKind.Sequential)] struct Size { public int width,height; }
        [StructLayout(LayoutKind.Sequential)] struct RECT { public int left,top,right,bottom; }
        [StructLayout(LayoutKind.Sequential,CharSet=CharSet.Unicode)] struct Version {
            public int size,major,minor,build,platform;
            [MarshalAs(UnmanagedType.ByValTStr,SizeConst=128)] public string service;
        }
        [StructLayout(LayoutKind.Sequential)] struct TextureDescription {
            public uint width,height,mips,array,format,samples,quality,usage,bind,cpu,misc;
        }
        [StructLayout(LayoutKind.Sequential)] struct Mapped { public IntPtr data; public uint rowPitch,depthPitch; }
        [DllImport("ntdll.dll",CharSet=CharSet.Unicode)] static extern int RtlGetVersion(ref Version version);
        [DllImport("combase.dll")] static extern int RoInitialize(uint mode);
        [DllImport("combase.dll")] static extern void RoUninitialize();
        [DllImport("combase.dll",CharSet=CharSet.Unicode)] static extern int WindowsCreateString(string text,int length,out IntPtr value);
        [DllImport("combase.dll")] static extern int WindowsDeleteString(IntPtr value);
        [DllImport("combase.dll")] static extern int RoGetActivationFactory(IntPtr name,ref Guid iid,out IntPtr factory);
        [DllImport("d3d11.dll")] static extern int D3D11CreateDevice(IntPtr adapter,uint driver,IntPtr software,uint flags,IntPtr levels,uint count,uint sdk,out IntPtr device,out uint level,out IntPtr context);
        [DllImport("d3d11.dll")] static extern int CreateDirect3D11DeviceFromDXGIDevice(IntPtr dxgi,out IntPtr device);
        [DllImport("dwmapi.dll")] static extern int DwmGetWindowAttribute(IntPtr hwnd,int attribute,out RECT value,int size);
        [DllImport("user32.dll")] static extern bool GetWindowDisplayAffinity(IntPtr hwnd,out uint affinity);
        [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate int NoArguments(IntPtr self);
        [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate void FlushContext(IntPtr self);
        [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate int GetBoolean(IntPtr self,out byte value);
        [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate int GetPointer(IntPtr self,out IntPtr value);
        [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate int GetSize(IntPtr self,out Size value);
        [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate int CreateItem(IntPtr self,IntPtr hwnd,ref Guid iid,out IntPtr item);
        [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate int CreatePool(IntPtr self,IntPtr device,int format,int buffers,Size size,out IntPtr pool);
        [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate int CreateSession(IntPtr self,IntPtr item,out IntPtr session);
        [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate int GetInterface(IntPtr self,ref Guid iid,out IntPtr value);
        [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate void GetDescription(IntPtr self,out TextureDescription value);
        [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate int CreateTexture(IntPtr self,ref TextureDescription description,IntPtr data,out IntPtr texture);
        [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate void CopyResource(IntPtr self,IntPtr destination,IntPtr source);
        [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate int MapResource(IntPtr self,IntPtr resource,uint subresource,uint mode,uint flags,out Mapped value);
        [UnmanagedFunctionPointer(CallingConvention.StdCall)] delegate void UnmapResource(IntPtr self,IntPtr resource,uint subresource);

        static readonly Guid ItemInterop=new Guid("3628e81b-3cac-4c60-b7f4-23ce0e0c3356");
        static readonly Guid Item=new Guid("79c3f95b-31f7-4ec2-a464-632ef5d30760");
        static readonly Guid PoolStatics=new Guid("589b103f-6bbc-5df5-a991-02e28b3b66d5");
        static readonly Guid SessionStatics=new Guid("2224a540-5974-49aa-b232-0882536f4cb5");
        static readonly Guid DxgiDevice=new Guid("54ec77fa-1377-44e6-8c32-88fd5f44c84c");
        static readonly Guid DirectDevice=new Guid("a37624ab-8d5f-4650-9d3e-9eae3d9bc670");
        static readonly Guid SurfaceAccess=new Guid("a9b3d012-3df2-4ee3-b8d1-8695f457d3c1");
        static readonly Guid Texture=new Guid("6f15aaf2-d208-4e89-9ab4-489535d34f9c");
        static readonly Guid Closable=new Guid("30d5a829-7fa4-4026-83bb-d75bae4ea99e");
        static T Method<T>(IntPtr instance,int slot) where T:class {
            return (T)(object)Marshal.GetDelegateForFunctionPointer(Marshal.ReadIntPtr(Marshal.ReadIntPtr(instance),slot*IntPtr.Size),typeof(T));
        }
        static void Check(int hr,string fallback) {
            if(hr>=0)return;
            if(hr==unchecked((int)0x80070005))throw new CaptureException("WGC_ACCESS_DENIED");
            if(hr==unchecked((int)0x887A0005) || hr==unchecked((int)0x887A0006) || hr==unchecked((int)0x887A0007))throw new CaptureException("WGC_DEVICE_LOST");
            throw new CaptureException(fallback);
        }
        static void Release(ref IntPtr value) { if(value!=IntPtr.Zero){Marshal.Release(value);value=IntPtr.Zero;} }
        static IntPtr Query(IntPtr value,Guid iid) { IntPtr result;Check(Marshal.QueryInterface(value,ref iid,out result),"WGC_API_UNAVAILABLE");return result; }
        static void Close(IntPtr value) {
            if(value==IntPtr.Zero)return;
            IntPtr closable=IntPtr.Zero;Guid iid=Closable;
            try { if(Marshal.QueryInterface(value,ref iid,out closable)>=0)Method<NoArguments>(closable,6)(closable); }
            finally { Release(ref closable); }
        }
        static IntPtr Factory(string name,Guid iid) {
            IntPtr text=IntPtr.Zero,result;
            try {Check(WindowsCreateString(name,name.Length,out text),"WGC_API_UNAVAILABLE");Check(RoGetActivationFactory(text,ref iid,out result),"WGC_API_UNAVAILABLE");return result;}
            finally {if(text!=IntPtr.Zero)WindowsDeleteString(text);}
        }
        static int Build() { Version version=new Version();version.size=Marshal.SizeOf(typeof(Version));return RtlGetVersion(ref version)==0 ? version.build : 0; }
        static bool InitializeRuntime() {
            int result=RoInitialize(1);
            // An already initialized STA can use these agile/free-threaded APIs.
            if(result==unchecked((int)0x80010106))return false;
            Check(result,"WGC_API_UNAVAILABLE");return true;
        }
        static void CreateDevice(out IntPtr device,out IntPtr context,out IntPtr direct) {
            IntPtr dxgi=IntPtr.Zero,inspectable=IntPtr.Zero;uint level;
            device=IntPtr.Zero;context=IntPtr.Zero;direct=IntPtr.Zero;
            try {
                Check(D3D11CreateDevice(IntPtr.Zero,1,IntPtr.Zero,0x20,IntPtr.Zero,0,7,out device,out level,out context),"WGC_DEVICE_UNAVAILABLE");
                dxgi=Query(device,DxgiDevice);
                Check(CreateDirect3D11DeviceFromDXGIDevice(dxgi,out inspectable),"WGC_DEVICE_UNAVAILABLE");
                direct=Query(inspectable,DirectDevice);
            } finally {Release(ref inspectable);Release(ref dxgi);}
        }
        static void CheckSupport(out IntPtr interop,out IntPtr poolFactory) {
            IntPtr session=IntPtr.Zero;interop=IntPtr.Zero;poolFactory=IntPtr.Zero;
            try {
                session=Factory("Windows.Graphics.Capture.GraphicsCaptureSession",SessionStatics);
                byte supported;Check(Method<GetBoolean>(session,6)(session,out supported),"WGC_UNSUPPORTED");
                if(supported==0)throw new CaptureException("WGC_UNSUPPORTED");
                interop=Factory("Windows.Graphics.Capture.GraphicsCaptureItem",ItemInterop);
                poolFactory=Factory("Windows.Graphics.Capture.Direct3D11CaptureFramePool",PoolStatics);
            } finally {Release(ref session);}
        }
        public static WgcProbe Probe() {
            WgcProbe result=new WgcProbe();result.reason="WGC_API_UNAVAILABLE";
            IntPtr interop=IntPtr.Zero,pool=IntPtr.Zero,device=IntPtr.Zero,context=IntPtr.Zero,direct=IntPtr.Zero;bool initialized=false;
            try {
                result.os_build=Build();if(result.os_build<18362){result.reason="WGC_OS_UNSUPPORTED";return result;}
                initialized=InitializeRuntime();CheckSupport(out interop,out pool);CreateDevice(out device,out context,out direct);
                result.available=true;result.reason="available";
            } catch(CaptureException error){result.reason=error.Code;}
            catch(DllNotFoundException){}catch(EntryPointNotFoundException){}
            finally {Release(ref direct);Release(ref context);Release(ref device);Release(ref pool);Release(ref interop);if(initialized)RoUninitialize();}
            return result;
        }
        public static CaptureRect WindowRect(IntPtr hwnd) {
            RECT rect;Check(DwmGetWindowAttribute(hwnd,9,out rect,Marshal.SizeOf(typeof(RECT))),"WGC_GEOMETRY_UNAVAILABLE");
            long width=(long)rect.right-rect.left,height=(long)rect.bottom-rect.top;
            if(width<1 || height<1 || width>16384 || height>16384)throw new CaptureException("SCREENSHOT_PIXEL_LIMIT");
            return new CaptureRect{x=rect.left,y=rect.top,width=(int)width,height=(int)height};
        }
        static void Deadline(Stopwatch clock,int timeout) { if(clock.ElapsedMilliseconds>=timeout)throw new CaptureException("SCREENSHOT_TIMEOUT"); }
        public static byte[] EncodeBgra(IntPtr pixels,int rowPitch,int width,int height,int maxPixels,int maxBytes) {
            Native.ValidateDimensions(width,height,maxPixels);
            if(pixels==IntPtr.Zero || rowPitch<checked(width*4) || (long)rowPitch*height>Int32.MaxValue)throw new CaptureException("WGC_INVALID_BUFFER");
            using(Bitmap bitmap=new Bitmap(width,height,PixelFormat.Format32bppPArgb)) {
                BitmapData target=bitmap.LockBits(new Rectangle(0,0,width,height),ImageLockMode.WriteOnly,PixelFormat.Format32bppPArgb);
                try {
                    byte[] row=new byte[checked(width*4)];
                    for(int y=0;y<height;y++){Marshal.Copy(IntPtr.Add(pixels,checked(y*rowPitch)),row,0,row.Length);Marshal.Copy(row,0,IntPtr.Add(target.Scan0,checked(y*target.Stride)),row.Length);}
                } finally {bitmap.UnlockBits(target);}
                return Native.Encode(bitmap,maxBytes);
            }
        }
        public static byte[] Capture(IntPtr hwnd,int width,int height,int maxPixels,int maxBytes,int timeout) {
            Native.ValidateDimensions(width,height,maxPixels);Stopwatch clock=Stopwatch.StartNew();
            IntPtr interop=IntPtr.Zero,factory=IntPtr.Zero,device=IntPtr.Zero,context=IntPtr.Zero,direct=IntPtr.Zero,item=IntPtr.Zero,pool=IntPtr.Zero,session=IntPtr.Zero,frame=IntPtr.Zero,surface=IntPtr.Zero,access=IntPtr.Zero,texture=IntPtr.Zero,staging=IntPtr.Zero;
            bool initialized=false,mapped=false;
            try {
                if(Build()<18362)throw new CaptureException("WGC_OS_UNSUPPORTED");
                initialized=InitializeRuntime();CheckSupport(out interop,out factory);CreateDevice(out device,out context,out direct);Deadline(clock,timeout);
                uint affinity;if(GetWindowDisplayAffinity(hwnd,out affinity) && affinity!=0)throw new CaptureException("WGC_WINDOW_EXCLUDED");
                Guid itemId=Item;Check(Method<CreateItem>(interop,3)(interop,hwnd,ref itemId,out item),"WGC_WINDOW_UNSUPPORTED");
                Size size;Check(Method<GetSize>(item,7)(item,out size),"WGC_GEOMETRY_UNAVAILABLE");
                if(size.width!=width || size.height!=height)throw new CaptureException("WINDOW_GEOMETRY_CHANGED");
                Native.ValidateDimensions(size.width,size.height,maxPixels);
                Check(Method<CreatePool>(factory,6)(factory,direct,87,1,size,out pool),"WGC_UNSUPPORTED");
                Check(Method<CreateSession>(pool,10)(pool,item,out session),"WGC_WINDOW_UNSUPPORTED");
                Check(Method<NoArguments>(session,6)(session),"WGC_ACCESS_DENIED");
                while(frame==IntPtr.Zero){Deadline(clock,timeout);Check(Method<GetPointer>(pool,7)(pool,out frame),"WGC_FRAME_UNAVAILABLE");if(frame==IntPtr.Zero)Thread.Sleep(5);}
                Size content;Check(Method<GetSize>(frame,8)(frame,out content),"WGC_FRAME_UNAVAILABLE");
                if(content.width!=width || content.height!=height)throw new CaptureException("WINDOW_GEOMETRY_CHANGED");
                Check(Method<GetPointer>(frame,6)(frame,out surface),"WGC_FRAME_UNAVAILABLE");access=Query(surface,SurfaceAccess);
                Guid textureId=Texture;Check(Method<GetInterface>(access,3)(access,ref textureId,out texture),"WGC_FRAME_UNAVAILABLE");
                TextureDescription description;Method<GetDescription>(texture,10)(texture,out description);
                if(description.width!=width || description.height!=height || description.format!=87 || description.samples!=1 || description.array!=1 || description.mips!=1)throw new CaptureException("WGC_INVALID_BUFFER");
                description.usage=3;description.bind=0;description.cpu=0x20000;description.misc=0;
                Check(Method<CreateTexture>(device,5)(device,ref description,IntPtr.Zero,out staging),"WGC_DEVICE_UNAVAILABLE");
                Method<CopyResource>(context,47)(context,staging,texture);
                Method<FlushContext>(context,111)(context);
                Mapped pixels;
                while(true){Deadline(clock,timeout);int hr=Method<MapResource>(context,14)(context,staging,0,1,0x100000,out pixels);if(hr==unchecked((int)0x887A000A)){Thread.Sleep(5);continue;}Check(hr,"WGC_READBACK_FAILED");mapped=true;break;}
                if(pixels.rowPitch>Int32.MaxValue)throw new CaptureException("WGC_INVALID_BUFFER");
                byte[] png=EncodeBgra(pixels.data,(int)pixels.rowPitch,width,height,maxPixels,maxBytes);Deadline(clock,timeout);return png;
            } catch(DllNotFoundException){throw new CaptureException("WGC_API_UNAVAILABLE");}
            catch(EntryPointNotFoundException){throw new CaptureException("WGC_API_UNAVAILABLE");}
            finally {
                if(mapped)Method<UnmapResource>(context,15)(context,staging,0);
                Release(ref staging);Release(ref texture);Release(ref access);Release(ref surface);Close(frame);Release(ref frame);
                Close(session);Release(ref session);Close(pool);Release(ref pool);Release(ref item);Release(ref direct);Release(ref context);Release(ref device);Release(ref factory);Release(ref interop);
                if(initialized)RoUninitialize();
            }
        }
    }
}
