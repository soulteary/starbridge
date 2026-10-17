function Initialize-DevScreenshotNative {
    if ('StarbridgeDeveloperScreenshot.Native' -as [type]) { return }
    Add-Type -ReferencedAssemblies @('System.dll', 'System.Drawing.dll') -TypeDefinition @'
using System;
using System.Drawing;
using System.Drawing.Imaging;
using System.IO;
using System.Runtime.InteropServices;
namespace StarbridgeDeveloperScreenshot {
    public sealed class CaptureException : Exception {
        public string Code { get; private set; }
        public CaptureException(string code) : base(code) { Code=code; }
    }
    // Image encoders may seek or write in chunks. Reject both operations before
    // growing the backing stream past the caller's compressed output budget.
    public sealed class BudgetStream : Stream {
        private readonly MemoryStream inner=new MemoryStream();
        private readonly long limit;
        public bool LimitExceeded { get; private set; }
        public BudgetStream(long maximum) { limit=maximum; }
        private void Exceeded() { LimitExceeded=true;throw new CaptureException("SCREENSHOT_BYTE_LIMIT"); }
        public override bool CanRead { get { return true; } }
        public override bool CanSeek { get { return true; } }
        public override bool CanWrite { get { return true; } }
        public override long Length { get { return inner.Length; } }
        public override long Position {
            get { return inner.Position; }
            set { if(value<0 || value>limit)Exceeded();inner.Position=value; }
        }
        public override void Flush() { inner.Flush(); }
        public override int Read(byte[] buffer,int offset,int count) { return inner.Read(buffer,offset,count); }
        public override long Seek(long offset,SeekOrigin origin) {
            long basis=origin==SeekOrigin.Begin ? 0 : origin==SeekOrigin.Current ? inner.Position : inner.Length;
            long destination=checked(basis+offset);
            Position=destination;return destination;
        }
        public override void SetLength(long value) {
            if(value<0 || value>limit)Exceeded();inner.SetLength(value);
        }
        public override void Write(byte[] buffer,int offset,int count) {
            if(count<0 || inner.Position>limit-count)Exceeded();inner.Write(buffer,offset,count);
        }
        public override void WriteByte(byte value) {
            if(inner.Position>=limit)Exceeded();inner.WriteByte(value);
        }
        public byte[] ToArray() { return inner.ToArray(); }
        protected override void Dispose(bool disposing) { if(disposing)inner.Dispose();base.Dispose(disposing); }
    }
    public static class Native {
        [DllImport("user32.dll")] private static extern bool PrintWindow(IntPtr hwnd,IntPtr dc,uint flags);
        public static void ValidateDimensions(int width,int height,int maxPixels) {
            if(width<1 || height<1 || width>16384 || height>16384 || (long)width*height>maxPixels)
                throw new CaptureException("SCREENSHOT_PIXEL_LIMIT");
        }
        public static byte[] Encode(Bitmap bitmap,int maxBytes) {
            using(BudgetStream stream=new BudgetStream(maxBytes)) {
                try { bitmap.Save(stream,ImageFormat.Png); }
                catch(CaptureException) { throw; }
                catch(Exception) {
                    // GDI+ can wrap stream exceptions. A budget-sized stream
                    // still means no larger output should be returned.
                    if(stream.LimitExceeded)throw new CaptureException("SCREENSHOT_BYTE_LIMIT");
                    throw new CaptureException("SCREENSHOT_ENCODE_FAILED");
                }
                return stream.ToArray();
            }
        }
        public static byte[] Capture(IntPtr hwnd,int width,int height,int maxPixels,int maxBytes) {
            ValidateDimensions(width,height,maxPixels);
            using(Bitmap bitmap=new Bitmap(width,height,PixelFormat.Format24bppRgb)) {
                using(Graphics graphics=Graphics.FromImage(bitmap)) {
                    graphics.Clear(Color.Black);
                    IntPtr dc=graphics.GetHdc();
                    try { if(!PrintWindow(hwnd,dc,0))throw new CaptureException("SCREENSHOT_RENDER_FAILED"); }
                    finally { graphics.ReleaseHdc(dc); }
                }
                return Encode(bitmap,maxBytes);
            }
        }
    }
}
'@
}
