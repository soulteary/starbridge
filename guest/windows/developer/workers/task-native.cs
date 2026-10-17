using System;
using System.Collections;
using System.Collections.Generic;
using System.ComponentModel;
using System.Diagnostics;
using System.IO;
using System.Runtime.InteropServices;
using System.Text;
using System.Threading;
using System.Threading.Tasks;
using Microsoft.Win32.SafeHandles;

namespace Starbridge.Developer {
    // The worker owns a non-breakaway, kill-on-close job. The child is assigned
    // while suspended so it cannot create a descendant before job ownership.
    public sealed class TaskProcess : IDisposable {
        [StructLayout(LayoutKind.Sequential)] struct SECURITY_ATTRIBUTES { public int length; public IntPtr descriptor; [MarshalAs(UnmanagedType.Bool)] public bool inherit; }
        [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)] struct STARTUPINFO {
            public int cb; public string reserved, desktop, title; public int x,y,xSize,ySize,xCount,yCount,fill,flags; public short show,reservedSize; public IntPtr reserved2,input,output,error;
        }
        [StructLayout(LayoutKind.Sequential)] struct PROCESS_INFORMATION { public IntPtr process,thread; public uint pid,tid; }
        [StructLayout(LayoutKind.Sequential)] struct BASIC_LIMITS { public long processTime,jobTime; public uint flags; public UIntPtr minWorking,maxWorking; public uint activeLimit; public UIntPtr affinity; public uint priority,scheduling; }
        [StructLayout(LayoutKind.Sequential)] struct IO_COUNTERS { public ulong readOps,writeOps,otherOps,readBytes,writeBytes,otherBytes; }
        [StructLayout(LayoutKind.Sequential)] struct EXTENDED_LIMITS { public BASIC_LIMITS basic; public IO_COUNTERS io; public UIntPtr processMemory,jobMemory,peakProcess,peakJob; }
        [StructLayout(LayoutKind.Sequential)] struct ACCOUNTING { public long userTime,kernelTime,periodUser,periodKernel; public uint pageFaults,totalProcesses,activeProcesses,terminated; }
        [DllImport("kernel32.dll",SetLastError=true)] static extern IntPtr CreateJobObject(IntPtr attributes,string name);
        [DllImport("kernel32.dll",SetLastError=true)] static extern bool SetInformationJobObject(IntPtr job,int info,IntPtr data,uint length);
        [DllImport("kernel32.dll",SetLastError=true)] static extern bool QueryInformationJobObject(IntPtr job,int info,out ACCOUNTING data,uint length,IntPtr returned);
        [DllImport("kernel32.dll",SetLastError=true)] static extern bool AssignProcessToJobObject(IntPtr job,IntPtr process);
        [DllImport("kernel32.dll",SetLastError=true)] static extern bool TerminateJobObject(IntPtr job,uint code);
        [DllImport("kernel32.dll",SetLastError=true)] static extern bool TerminateProcess(IntPtr process,uint code);
        [DllImport("kernel32.dll",SetLastError=true)] static extern bool CreatePipe(out IntPtr read,out IntPtr write,ref SECURITY_ATTRIBUTES attributes,uint size);
        [DllImport("kernel32.dll",SetLastError=true)] static extern bool SetHandleInformation(IntPtr handle,uint mask,uint flags);
        [DllImport("kernel32.dll",CharSet=CharSet.Unicode,SetLastError=true)] static extern IntPtr CreateFile(string name,uint access,uint share,ref SECURITY_ATTRIBUTES attributes,uint creation,uint flags,IntPtr template);
        [DllImport("kernel32.dll",CharSet=CharSet.Unicode,SetLastError=true)] static extern bool CreateProcess(string application,StringBuilder command,IntPtr processAttributes,IntPtr threadAttributes,bool inherit,uint flags,IntPtr environment,string directory,ref STARTUPINFO startup,out PROCESS_INFORMATION process);
        [DllImport("kernel32.dll",SetLastError=true)] static extern uint ResumeThread(IntPtr thread);
        [DllImport("kernel32.dll",SetLastError=true)] static extern bool GetExitCodeProcess(IntPtr process,out uint code);
        [DllImport("kernel32.dll",SetLastError=true)] static extern bool GetProcessTimes(IntPtr process,out long created,out long exited,out long kernel,out long user);
        [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr handle);
        IntPtr job,process;
        FileStream outputReader,errorReader;
        Task outputTask,errorTask;
        long stdoutBytes,stderrBytes;
        volatile bool stdoutTruncated,stderrTruncated;
        public string Stage { get; private set; }
        public int Pid { get; private set; }
        public long StartTimeTicks { get; private set; }
        public string Executable { get; private set; }
        public long StdoutBytes { get { return Interlocked.Read(ref stdoutBytes); } }
        public long StderrBytes { get { return Interlocked.Read(ref stderrBytes); } }
        public bool StdoutTruncated { get { return stdoutTruncated; } }
        public bool StderrTruncated { get { return stderrTruncated; } }
        public bool IsComplete { get {
            ACCOUNTING info;
            if (!QueryInformationJobObject(job,1,out info,(uint)Marshal.SizeOf(typeof(ACCOUNTING)),IntPtr.Zero)) throw new Win32Exception(Marshal.GetLastWin32Error());
            return info.activeProcesses==0;
        } }
        public int ExitCode { get { uint code; if (!GetExitCodeProcess(process,out code)) throw new Win32Exception(Marshal.GetLastWin32Error()); return unchecked((int)code); } }
        static void Check(bool condition) { if(!condition) throw new Win32Exception(Marshal.GetLastWin32Error()); }
        static void Close(ref IntPtr handle) { if(handle!=IntPtr.Zero && handle!=new IntPtr(-1)) CloseHandle(handle); handle=IntPtr.Zero; }
        public static string QuoteArgument(string text) {
            // Microsoft CRT argv quoting: double backslashes before quotes and
            // before the final quote. Every argument, including empty, is quoted.
            StringBuilder result=new StringBuilder("\""); int slashes=0;
            foreach(char c in text) {
                if(c=='\\') {slashes++;continue;}
                if(c=='"') {result.Append('\\',slashes*2+1);result.Append(c);slashes=0;continue;}
                result.Append('\\',slashes);slashes=0;result.Append(c);
            }
            result.Append('\\',slashes*2);return result.Append('"').ToString();
        }
        public static StringBuilder BuildCommandLine(string executable,string[] arguments) {
            StringBuilder command=new StringBuilder(QuoteArgument(executable));
            foreach(string arg in arguments)command.Append(' ').Append(QuoteArgument(arg));
            // CreateProcessW counts UTF-16 code units, including the final NUL.
            // Use this same builder for preflight and both native launches.
            if(command.Length>32766)throw new ArgumentException("The Windows command line exceeds its length limit.");
            return command;
        }
        public void Start(string executable,string[] arguments,string directory,IDictionary environment,string stdout,string stderr,int cap,bool showWindow) {
            StringBuilder command=BuildCommandLine(executable,arguments);
            IntPtr outputRead=IntPtr.Zero,outputWrite=IntPtr.Zero,errorRead=IntPtr.Zero,errorWrite=IntPtr.Zero,input=IntPtr.Zero,environmentBlock=IntPtr.Zero,thread=IntPtr.Zero;
            try {
                Stage="create_job";
                job=CreateJobObject(IntPtr.Zero,null);if(job==IntPtr.Zero)throw new Win32Exception(Marshal.GetLastWin32Error());
                EXTENDED_LIMITS limits=new EXTENDED_LIMITS();limits.basic.flags=0x2000;
                IntPtr pointer=Marshal.AllocHGlobal(Marshal.SizeOf(typeof(EXTENDED_LIMITS)));
                try {Marshal.StructureToPtr(limits,pointer,false);Check(SetInformationJobObject(job,9,pointer,(uint)Marshal.SizeOf(typeof(EXTENDED_LIMITS))));}finally{Marshal.FreeHGlobal(pointer);}
                Stage="create_pipes";
                SECURITY_ATTRIBUTES attributes=new SECURITY_ATTRIBUTES();attributes.length=Marshal.SizeOf(typeof(SECURITY_ATTRIBUTES));attributes.inherit=true;
                Check(CreatePipe(out outputRead,out outputWrite,ref attributes,0));Check(SetHandleInformation(outputRead,1,0));
                Check(CreatePipe(out errorRead,out errorWrite,ref attributes,0));Check(SetHandleInformation(errorRead,1,0));
                input=CreateFile("NUL",0x80000000,3,ref attributes,3,0,IntPtr.Zero);if(input==new IntPtr(-1))throw new Win32Exception(Marshal.GetLastWin32Error());
                Stage="environment";
                SortedDictionary<string,string> variables=new SortedDictionary<string,string>(StringComparer.OrdinalIgnoreCase);
                foreach(DictionaryEntry pair in Environment.GetEnvironmentVariables())variables[(string)pair.Key]=(string)pair.Value;
                foreach(DictionaryEntry pair in environment)variables[(string)pair.Key]=(string)pair.Value;
                StringBuilder block=new StringBuilder();foreach(KeyValuePair<string,string> pair in variables)block.Append(pair.Key).Append('=').Append(pair.Value).Append('\0');block.Append('\0');
                environmentBlock=Marshal.StringToHGlobalUni(block.ToString());
                STARTUPINFO startup=new STARTUPINFO();startup.cb=Marshal.SizeOf(typeof(STARTUPINFO));startup.flags=0x100|1;startup.show=(short)(showWindow?1:0);startup.input=input;startup.output=outputWrite;startup.error=errorWrite;
                PROCESS_INFORMATION info;
                Stage="create_process";
                Check(CreateProcess(executable,command,IntPtr.Zero,IntPtr.Zero,true,0x4|0x400|0x8000000,environmentBlock,directory,ref startup,out info));
                process=info.process;thread=info.thread;Pid=(int)info.pid;Executable=executable;
                // Failure here leaves a suspended process, which is terminated by handle.
                Stage="assign_job";
                Check(AssignProcessToJobObject(job,process));
                Stage="process_identity";
                long created,exited,kernel,user;Check(GetProcessTimes(process,out created,out exited,out kernel,out user));StartTimeTicks=DateTime.FromFileTimeUtc(created).Ticks;
                Stage="capture_output";
                outputReader=new FileStream(new SafeFileHandle(outputRead,true),FileAccess.Read);outputRead=IntPtr.Zero;
                errorReader=new FileStream(new SafeFileHandle(errorRead,true),FileAccess.Read);errorRead=IntPtr.Zero;
                outputTask=Task.Factory.StartNew(delegate{Capture(outputReader,stdout,cap,true);},TaskCreationOptions.LongRunning);
                errorTask=Task.Factory.StartNew(delegate{Capture(errorReader,stderr,cap,false);},TaskCreationOptions.LongRunning);
                Stage="resume_process";
                if(ResumeThread(thread)==UInt32.MaxValue)throw new Win32Exception(Marshal.GetLastWin32Error());
                Stage="running";
            } catch {if(process!=IntPtr.Zero)TerminateProcess(process,1);Dispose();throw;}
            finally {Close(ref outputRead);Close(ref errorRead);Close(ref outputWrite);Close(ref errorWrite);Close(ref input);Close(ref thread);if(environmentBlock!=IntPtr.Zero)Marshal.FreeHGlobal(environmentBlock);}
        }
        void Capture(Stream input,string path,int cap,bool output) {
            using(FileStream file=new FileStream(path,FileMode.Create,FileAccess.Write,FileShare.ReadWrite)) {
                byte[] buffer=new byte[8192];int count;long stored=0;
                while((count=input.Read(buffer,0,buffer.Length))>0) {
                    int keep=(int)Math.Min(count,Math.Max(0,cap-stored));
                    if(keep>0){file.Write(buffer,0,keep);file.Flush();stored+=keep;}
                    if(output){Interlocked.Exchange(ref stdoutBytes,stored);if(keep<count)stdoutTruncated=true;}
                    else {Interlocked.Exchange(ref stderrBytes,stored);if(keep<count)stderrTruncated=true;}
                }
            }
        }
        public void FinishOutput() { Stage="finish_output"; if(outputTask!=null && errorTask!=null && !Task.WaitAll(new Task[]{outputTask,errorTask},5000))throw new IOException("Output pipes did not close."); }
        public void Cancel() {Check(TerminateJobObject(job,1));}
        public bool RequestClose() {
            using(Process child=Process.GetProcessById(Pid)) {
                if(child.StartTime.ToUniversalTime().Ticks!=StartTimeTicks || child.HasExited)return false;
                if(!String.Equals(Path.GetFullPath(child.MainModule.FileName),Path.GetFullPath(Executable),StringComparison.OrdinalIgnoreCase))return false;
                return child.CloseMainWindow();
            }
        }
        public void Dispose() {Close(ref job);Close(ref process);if(outputReader!=null)outputReader.Dispose();if(errorReader!=null)errorReader.Dispose();}
    }
    // A short-lived transport must not own the worker's standard streams.
    // Only these three NUL handles are inherited; transport pipes never cross.
    public sealed class DetachedWorker : IDisposable {
        [StructLayout(LayoutKind.Sequential)] struct SECURITY_ATTRIBUTES { public int length; public IntPtr descriptor; [MarshalAs(UnmanagedType.Bool)] public bool inherit; }
        [StructLayout(LayoutKind.Sequential,CharSet=CharSet.Unicode)] struct STARTUPINFO {
            public int cb; public IntPtr reserved,desktop,title; public int x,y,xSize,ySize,xCount,yCount,fill,flags; public short show,reservedSize; public IntPtr reserved2,input,output,error;
        }
        [StructLayout(LayoutKind.Sequential)] struct STARTUPINFOEX { public STARTUPINFO startup; public IntPtr attributes; }
        [StructLayout(LayoutKind.Sequential)] struct PROCESS_INFORMATION { public IntPtr process,thread; public uint pid,tid; }
        [DllImport("kernel32.dll",CharSet=CharSet.Unicode,SetLastError=true)] static extern IntPtr CreateFile(string name,uint access,uint share,ref SECURITY_ATTRIBUTES attributes,uint creation,uint flags,IntPtr template);
        [DllImport("kernel32.dll",SetLastError=true)] static extern bool InitializeProcThreadAttributeList(IntPtr list,int count,int flags,ref IntPtr size);
        [DllImport("kernel32.dll",SetLastError=true)] static extern bool UpdateProcThreadAttribute(IntPtr list,uint flags,IntPtr attribute,IntPtr value,IntPtr size,IntPtr previous,IntPtr returned);
        [DllImport("kernel32.dll")] static extern void DeleteProcThreadAttributeList(IntPtr list);
        [DllImport("kernel32.dll",CharSet=CharSet.Unicode,SetLastError=true)] static extern bool CreateProcess(string application,StringBuilder command,IntPtr processAttributes,IntPtr threadAttributes,bool inherit,uint flags,IntPtr environment,string directory,ref STARTUPINFOEX startup,out PROCESS_INFORMATION process);
        [DllImport("kernel32.dll",SetLastError=true)] static extern bool GetProcessTimes(IntPtr process,out long created,out long exited,out long kernel,out long user);
        [DllImport("kernel32.dll",CharSet=CharSet.Unicode,SetLastError=true)] static extern bool QueryFullProcessImageName(IntPtr process,int flags,StringBuilder name,ref int size);
        [DllImport("kernel32.dll",SetLastError=true)] static extern uint ResumeThread(IntPtr thread);
        [DllImport("kernel32.dll",SetLastError=true)] static extern bool TerminateProcess(IntPtr process,uint code);
        [DllImport("kernel32.dll")] static extern bool CloseHandle(IntPtr handle);
        IntPtr process,thread;
        bool resumed;
        public int Pid { get; private set; }
        public long StartTimeTicks { get; private set; }
        public string Executable { get; private set; }
        static void Check(bool value) { if(!value)throw new Win32Exception(Marshal.GetLastWin32Error()); }
        static void Close(IntPtr value) { if(value!=IntPtr.Zero && value!=new IntPtr(-1))CloseHandle(value); }
        public void Start(string executable,string[] arguments,string directory) {
            StringBuilder command=TaskProcess.BuildCommandLine(executable,arguments);
            IntPtr input=IntPtr.Zero,output=IntPtr.Zero,error=IntPtr.Zero,list=IntPtr.Zero,handles=IntPtr.Zero;
            bool initialized=false;
            try {
                SECURITY_ATTRIBUTES security=new SECURITY_ATTRIBUTES();security.length=Marshal.SizeOf(typeof(SECURITY_ATTRIBUTES));security.inherit=true;
                input=CreateFile("NUL",0x80000000,3,ref security,3,0,IntPtr.Zero);if(input==new IntPtr(-1))throw new Win32Exception(Marshal.GetLastWin32Error());
                output=CreateFile("NUL",0x40000000,3,ref security,3,0,IntPtr.Zero);if(output==new IntPtr(-1))throw new Win32Exception(Marshal.GetLastWin32Error());
                error=CreateFile("NUL",0x40000000,3,ref security,3,0,IntPtr.Zero);if(error==new IntPtr(-1))throw new Win32Exception(Marshal.GetLastWin32Error());
                IntPtr size=IntPtr.Zero;InitializeProcThreadAttributeList(IntPtr.Zero,1,0,ref size);
                list=Marshal.AllocHGlobal(size);Check(InitializeProcThreadAttributeList(list,1,0,ref size));initialized=true;
                handles=Marshal.AllocHGlobal(IntPtr.Size*3);Marshal.WriteIntPtr(handles,0,input);Marshal.WriteIntPtr(handles,IntPtr.Size,output);Marshal.WriteIntPtr(handles,IntPtr.Size*2,error);
                Check(UpdateProcThreadAttribute(list,0,new IntPtr(0x20002),handles,new IntPtr(IntPtr.Size*3),IntPtr.Zero,IntPtr.Zero));
                STARTUPINFOEX startup=new STARTUPINFOEX();startup.startup.cb=Marshal.SizeOf(typeof(STARTUPINFOEX));startup.startup.flags=0x100|1;startup.startup.show=0;startup.startup.input=input;startup.startup.output=output;startup.startup.error=error;startup.attributes=list;
                PROCESS_INFORMATION information;
                // Suspend until the caller has durably recorded the worker identity.
                Check(CreateProcess(executable,command,IntPtr.Zero,IntPtr.Zero,true,0x4|0x80000|0x8000000,IntPtr.Zero,directory,ref startup,out information));
                process=information.process;thread=information.thread;Pid=(int)information.pid;
                long created,exited,kernel,user;Check(GetProcessTimes(process,out created,out exited,out kernel,out user));StartTimeTicks=DateTime.FromFileTimeUtc(created).Ticks;
                StringBuilder image=new StringBuilder(32768);int length=image.Capacity;Check(QueryFullProcessImageName(process,0,image,ref length));Executable=image.ToString();
            } catch {Dispose();throw;}
            finally {Close(input);Close(output);Close(error);if(initialized)DeleteProcThreadAttributeList(list);if(list!=IntPtr.Zero)Marshal.FreeHGlobal(list);if(handles!=IntPtr.Zero)Marshal.FreeHGlobal(handles);}
        }
        public void Resume() {
            if(ResumeThread(thread)==UInt32.MaxValue)throw new Win32Exception(Marshal.GetLastWin32Error());
            resumed=true;Close(thread);thread=IntPtr.Zero;
        }
        public void Dispose() {
            // Failed receipt creation must not strand an unowned suspended worker.
            if(!resumed && process!=IntPtr.Zero)TerminateProcess(process,1);
            Close(thread);thread=IntPtr.Zero;Close(process);process=IntPtr.Zero;
        }
    }

}
