function Initialize-DevUiInputNative {
    if ('StarbridgeDeveloperInput.Native' -as [type]) { return }
    Add-Type -TypeDefinition @'
using System;
using System.Collections.Generic;
using System.Runtime.InteropServices;
using System.Text;
namespace StarbridgeDeveloperInput {
    public sealed class Rect { public int x,y,width,height; }
    public sealed class UnicodeInputEvent { public ushort VirtualKey,ScanCode; public uint Flags; }
    public static class Native {
        [StructLayout(LayoutKind.Sequential)] private struct RECT { public int left,top,right,bottom; }
        [StructLayout(LayoutKind.Sequential)] private struct POINT { public int x,y; public POINT(int a,int b) { x=a;y=b; } }
        [StructLayout(LayoutKind.Sequential)] private struct MOUSEINPUT { public int dx,dy; public uint mouseData,flags,time; public UIntPtr extra; }
        [StructLayout(LayoutKind.Sequential)] private struct KEYBDINPUT { public ushort vk,scan; public uint flags,time; public UIntPtr extra; }
        [StructLayout(LayoutKind.Explicit)] private struct UNION { [FieldOffset(0)]public MOUSEINPUT mouse;[FieldOffset(0)]public KEYBDINPUT keyboard; }
        [StructLayout(LayoutKind.Sequential)] private struct INPUT { public uint type; public UNION data; }
        [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow();
        [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr hwnd);
        [DllImport("user32.dll")] public static extern bool IsWindowEnabled(IntPtr hwnd);
        [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr hwnd);
        [DllImport("user32.dll")] private static extern bool SetForegroundWindow(IntPtr hwnd);
        [DllImport("user32.dll")] private static extern bool ShowWindow(IntPtr hwnd,int command);
        [DllImport("user32.dll")] private static extern bool SetWindowPos(IntPtr hwnd,IntPtr insert,int x,int y,int width,int height,uint flags);
        [DllImport("user32.dll")] private static extern bool GetWindowRect(IntPtr hwnd,out RECT rect);
        [DllImport("user32.dll")] private static extern bool GetClientRect(IntPtr hwnd,out RECT rect);
        [DllImport("user32.dll")] private static extern bool ClientToScreen(IntPtr hwnd,ref POINT point);
        [DllImport("user32.dll")] private static extern IntPtr WindowFromPoint(POINT point);
        [DllImport("user32.dll")] private static extern IntPtr GetAncestor(IntPtr hwnd,uint flags);
        [DllImport("user32.dll")] private static extern IntPtr GetProcessWindowStation();
        [DllImport("user32.dll")] private static extern IntPtr GetThreadDesktop(uint thread);
        [DllImport("user32.dll")] private static extern uint GetWindowThreadProcessId(IntPtr hwnd,out uint pid);
        [DllImport("kernel32.dll")] private static extern uint GetCurrentThreadId();
        [DllImport("user32.dll")] private static extern IntPtr OpenInputDesktop(uint flags,bool inherit,uint access);
        [DllImport("user32.dll")] private static extern bool CloseDesktop(IntPtr desktop);
        [DllImport("user32.dll",CharSet=CharSet.Unicode)] private static extern bool GetUserObjectInformation(IntPtr handle,int index,StringBuilder buffer,int bytes,out int required);
        [DllImport("user32.dll")] private static extern short GetAsyncKeyState(int key);
        [DllImport("user32.dll")] private static extern int GetSystemMetrics(int index);
        [DllImport("user32.dll",SetLastError=true)] private static extern uint SendInput(uint count,INPUT[] inputs,int bytes);

        private static string ObjectName(IntPtr handle) {
            StringBuilder value=new StringBuilder(257);int required;
            return handle!=IntPtr.Zero && GetUserObjectInformation(handle,2,value,value.Capacity*2,out required) ? value.ToString() : null;
        }
        public static bool InputDesktopAccessible(IntPtr hwnd) {
            if (!String.Equals(ObjectName(GetProcessWindowStation()),"WinSta0",StringComparison.OrdinalIgnoreCase)) return false;
            string current=ObjectName(GetThreadDesktop(GetCurrentThreadId()));
            IntPtr input=OpenInputDesktop(0,false,1);
            if(input==IntPtr.Zero) return false;
            try {
                uint pid;uint thread=GetWindowThreadProcessId(hwnd,out pid);
                string target=ObjectName(GetThreadDesktop(thread));string name=ObjectName(input);
                return current!=null && name!=null && target!=null && String.Equals(current,name,StringComparison.OrdinalIgnoreCase) && String.Equals(target,name,StringComparison.OrdinalIgnoreCase);
            }
            finally { CloseDesktop(input); }
        }
        public static bool RequestFocus(IntPtr hwnd) {
            if(IsIconic(hwnd)) ShowWindow(hwnd,9);
            return SetForegroundWindow(hwnd) && GetForegroundWindow()==hwnd;
        }
        public static Rect WindowRect(IntPtr hwnd) {
            RECT r;if(!GetWindowRect(hwnd,out r))return null;
            return new Rect{x=r.left,y=r.top,width=r.right-r.left,height=r.bottom-r.top};
        }
        public static bool Resize(IntPtr hwnd,int x,int y,int width,int height) { return SetWindowPos(hwnd,IntPtr.Zero,x,y,width,height,0x14); }
        public static bool ClientHit(IntPtr hwnd,int x,int y) {
            RECT r;POINT origin=new POINT(0,0);
            if(!GetClientRect(hwnd,out r)||!ClientToScreen(hwnd,ref origin))return false;
            if(x<origin.x||y<origin.y||x>=origin.x+r.right-r.left||y>=origin.y+r.bottom-r.top)return false;
            IntPtr hit=WindowFromPoint(new POINT(x,y));
            return hit!=IntPtr.Zero && GetAncestor(hit,2)==hwnd;
        }
        public static bool PhysicalInputIdle() {
            foreach(int key in new int[]{1,2,4,0x10,0x11,0x12,0x5b,0x5c,0xa0,0xa1,0xa2,0xa3,0xa4,0xa5})
                if((GetAsyncKeyState(key)&0x8000)!=0)return false;
            return true;
        }
        public static bool TextInputIdle() {
            for(int key=1;key<255;key++)if((GetAsyncKeyState(key)&0x8000)!=0)return false;
            return true;
        }
        public static int InputSize() { return Marshal.SizeOf(typeof(INPUT)); }
        private static uint Extended(ushort key) { return (key>=0x21 && key<=0x28)||key==0x2d||key==0x2e ? 1u : 0u; }
        private static INPUT Key(ushort key,bool up) {
            INPUT result=new INPUT();result.type=1;result.data.keyboard.vk=key;result.data.keyboard.flags=Extended(key)|(up?2u:0u);return result;
        }
        public static int KeyChord(ushort[] keys) {
            List<INPUT> events=new List<INPUT>();
            foreach(ushort key in keys)events.Add(Key(key,false));
            for(int i=keys.Length-1;i>=0;i--)events.Add(Key(keys[i],true));
            uint sent=SendInput((uint)events.Count,events.ToArray(),InputSize());
            if(sent!=(uint)events.Count) {
                // Release only this chord's keys after a partial insertion;
                // never resend the application action.
                List<INPUT> release=new List<INPUT>();
                for(int i=keys.Length-1;i>=0;i--)release.Add(Key(keys[i],true));
                SendInput((uint)release.Count,release.ToArray(),InputSize());
                return -1;
            }
            return events.Count;
        }
        // Pure event construction is separately testable without touching a
        // desktop. Preserve every scalar, including CR/LF/Tab, literally.
        public static UnicodeInputEvent[] BuildUnicodeTextEvents(string text) {
            if(String.IsNullOrEmpty(text)||text.Length>4096)throw new ArgumentException("Invalid Unicode text.");
            int points=0;
            for(int i=0;i<text.Length;i++) {
                char unit=text[i];
                if(unit==0)throw new ArgumentException("Invalid Unicode text.");
                if(Char.IsHighSurrogate(unit)) {
                    if(i+1>=text.Length||!Char.IsLowSurrogate(text[i+1]))throw new ArgumentException("Invalid Unicode text.");
                    i++;
                }else if(Char.IsLowSurrogate(unit))throw new ArgumentException("Invalid Unicode text.");
                if(++points>2048)throw new ArgumentException("Unicode text exceeds the code-point budget.");
            }
            UnicodeInputEvent[] events=new UnicodeInputEvent[text.Length*2];
            for(int i=0;i<text.Length;i++) {
                events[i*2]=new UnicodeInputEvent{VirtualKey=0,ScanCode=text[i],Flags=4};
                events[i*2+1]=new UnicodeInputEvent{VirtualKey=0,ScanCode=text[i],Flags=6};
            }
            return events;
        }
        private static int InsertUnicodeEvents(UnicodeInputEvent[] events) {
            INPUT[] inputs=new INPUT[events.Length];
            for(int i=0;i<events.Length;i++) {
                inputs[i].type=1;inputs[i].data.keyboard.vk=events[i].VirtualKey;
                inputs[i].data.keyboard.scan=events[i].ScanCode;inputs[i].data.keyboard.flags=events[i].Flags;
            }
            return (int)SendInput((uint)inputs.Length,inputs,InputSize());
        }
        // Injection through a delegate lets contract tests simulate a short
        // SendInput result. Production passes InsertUnicodeEvents exclusively.
        public static int DispatchUnicodeText(string text,Func<UnicodeInputEvent[],int> insert) {
            UnicodeInputEvent[] events=BuildUnicodeTextEvents(text);
            int sent;
            try { sent=insert(events); }catch { return -1; }
            if(sent==events.Length)return sent;
            if(sent>0&&sent<events.Length&&(sent%2)!=0) {
                // Only release the final unmatched Unicode down event. Never
                // resend a scalar, the rest of a surrogate pair, or all text.
                try { insert(new UnicodeInputEvent[]{events[sent]}); }catch {}
            }
            return -1;
        }
        public static int UnicodeText(string text) { return DispatchUnicodeText(text,InsertUnicodeEvents); }
        public static int NormalizeAbsolute(int position,int origin,int size) {
            if(size<=1||position<origin||(long)position>=(long)origin+size)throw new ArgumentOutOfRangeException();
            return (int)Math.Round(((long)position-origin)*65535.0/(size-1));
        }
        private static INPUT Mouse(uint flags,uint data,int x,int y) {
            INPUT result=new INPUT();result.type=0;result.data.mouse.flags=flags;result.data.mouse.mouseData=data;result.data.mouse.dx=x;result.data.mouse.dy=y;return result;
        }
        public static int MouseOperation(int x,int y,string operation,string button,int delta) {
            int nx=NormalizeAbsolute(x,GetSystemMetrics(76),GetSystemMetrics(78));
            int ny=NormalizeAbsolute(y,GetSystemMetrics(77),GetSystemMetrics(79));
            List<INPUT> events=new List<INPUT>();events.Add(Mouse(0xc001,0,nx,ny));
            if(operation=="click") {
                uint down=button=="right"?8u:button=="middle"?32u:2u;
                events.Add(Mouse(down,0,0,0));events.Add(Mouse(down*2,0,0,0));
            } else if(operation=="wheel")events.Add(Mouse(0x800,unchecked((uint)delta),0,0));
            uint sent=SendInput((uint)events.Count,events.ToArray(),InputSize());
            if(sent!=(uint)events.Count) {
                if(operation=="click") {
                    uint up=button=="right"?16u:button=="middle"?64u:4u;
                    SendInput(1,new INPUT[]{Mouse(up,0,0,0)},InputSize());
                }
                return -1;
            }
            return events.Count;
        }
    }
}
'@
}
