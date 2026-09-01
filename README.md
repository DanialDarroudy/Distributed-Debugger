# Causally Consistent Reversible Debugger for MPI

A reversible debugger for MPI applications that allows stepping backwards through program execution using process
checkpointing.

> ⚠️ **Architecture Notice:** Only `x86_64` is currently supported.  
> Since CRIU requires root privileges, it is strongly recommended to run this inside a **container** or **virtual
machine**.

---

## Table of Contents

- [Requirements](#requirements)
- [Building the Toolchain from Source](#building-the-toolchain-from-source)
    - [1. GCC 8.5.0](#1-gcc-850)
    - [2. MPICH 3.4.3](#2-mpich-343)
    - [3. Go 1.21](#3-go-121)
    - [4. CRIU 3.19](#4-criu-319)
- [Building the Debugger](#building-the-debugger)
- [Usage](#usage)
    - [Compile Your Program](#compile-your-program)
    - [Run the Debugger](#run-the-debugger)
    - [Accessing the Web GUI](#accessing-the-web-gui)
    - [Example Programs](#example-programs)
- [Environment Variables](#environment-variables)
- [Backends](#backends)

---

## Requirements

This debugger has been tested with the following **exact** versions.  
Using newer versions (e.g., GCC 13 on Ubuntu 24.04) will cause compatibility issues with the DWARF debug metadata
parser.

| Tool  | Version | Why this version?                     |
|-------|---------|---------------------------------------|
| GCC   | 8.5.0   | Must produce DWARF level 4 debug data |
| MPICH | 3.4.3   | Tested MPI implementation             |
| Go    | 1.21    | Required for building the debugger    |
| CRIU  | 3.19    | Process checkpointing backend         |

> **Note for Ubuntu 24.04 users:**  
> Ubuntu 24.04 ships with GCC 13, Go 1.22+, and does not include CRIU in its official repositories. All four tools above
> must be built from source. Follow the steps below carefully.

---

## Building the Toolchain from Source

First, install the base dependencies needed for all builds:

```bash
sudo apt-get update
sudo apt-get install -y build-essential wget curl libgmp-dev libmpfr-dev \
    libmpc-dev flex bison pkg-config libprotobuf-dev libprotobuf-c-dev \
    protobuf-c-compiler protobuf-compiler python3-protobuf libnl-3-dev \
    libnet-dev libcap-dev bsdmainutils asciidoc npm
```

---

### 1. GCC 8.5.0

The debugger's DWARF parser requires executables compiled with DWARF level 4.  
GCC 8.5.0 produces this format by default, while newer versions produce DWARF 5.

```bash
mkdir -p ~/toolchain/gcc-build && cd ~/toolchain/gcc-build

# Download source
wget https://ftpmirror.gnu.org/gcc/gcc-8.5.0/gcc-8.5.0.tar.gz
tar xzf gcc-8.5.0.tar.gz
cd gcc-8.5.0

# Download internal dependencies
./contrib/download_prerequisites

# Configure and build (use -j$(nproc) to use all available CPU cores)
cd ..
mkdir objdir && cd objdir
../gcc-8.5.0/configure \
    --prefix=/usr/local/gcc-8.5.0 \
    --enable-languages=c,c++ \
    --disable-multilib

make -j$(nproc)
sudo make install
```

Add GCC 8.5.0 to your PATH:

```bash
export PATH=/usr/local/gcc-8.5.0/bin:$PATH
```

Verify installation:

```bash
gcc --version
# Expected: gcc (GCC) 8.5.0
```

---

### 2. MPICH 3.4.3

MPICH must be built using GCC 8.5.0 to ensure consistent DWARF metadata.

```bash
cd ~/toolchain

# Download source
wget https://www.mpich.org/static/downloads/3.4.3/mpich-3.4.3.tar.gz
tar xzf mpich-3.4.3.tar.gz
cd mpich-3.4.3

# Point to GCC 8.5.0
export CC=/usr/local/gcc-8.5.0/bin/gcc
export CXX=/usr/local/gcc-8.5.0/bin/g++

# Configure and build
./configure \
    --prefix=/usr/local/mpich-3.4.3 \
    --disable-fortran

make -j$(nproc)
sudo make install
```

Add MPICH to your PATH:

```bash
export PATH=/usr/local/mpich-3.4.3/bin:$PATH
```

Verify installation:

```bash
mpicc -v
# Should reference GCC 8.5.0
```

---

### 3. Go 1.21

```bash
cd ~/toolchain

wget https://go.dev/dl/go1.21.0.linux-amd64.tar.gz
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf go1.21.0.linux-amd64.tar.gz
```

Add Go to your PATH:

```bash
export PATH=/usr/local/go/bin:$PATH
```

Verify installation:

```bash
go version
# Expected: go version go1.21.0 linux/amd64
```

---

### 4. CRIU 3.19

CRIU is the checkpointing backend used to snapshot and restore MPI processes.  
It is not available in Ubuntu 24.04's official repositories and must be built from source using GCC 8.5.0.

```bash
cd ~/toolchain

# Download source
wget https://github.com/checkpoint-restore/criu/archive/refs/tags/v3.19.tar.gz
tar -xzf v3.19.tar.gz
cd criu-3.19

# Use GCC 8.5.0
export CC=/usr/local/gcc-8.5.0/bin/gcc
export HOSTCC=/usr/local/gcc-8.5.0/bin/gcc

# Build and install
make
sudo make install
```

Verify installation:

```bash
criu --version
# Expected: Version: 3.19
```

---

## Building the Debugger

Once all toolchain components are installed, clone the repository and build:

```bash
git clone https://github.com/mihkeltiks/rev-mpi-deb.git
cd rev-mpi-deb

make
```

This builds the following binaries into `./bin/`:

| Binary              | Description                   |
|---------------------|-------------------------------|
| `bin/orchestrator`  | Main debugger process manager |
| `bin/compiler`      | MPI wrapper compiler          |
| `bin/node-debugger` | Per-process debug agent       |

---

## Usage

### Compile Your Program

Programs must be compiled with the included compiler wrapper.  
This instruments the MPI library calls so the debugger can intercept and record them.

```bash
bin/compiler <path-to-your-mpi-program>
```

The compiled binary will be written to:

```
./bin/targets/<source-file-name>
```

**Example:**

```bash
bin/compiler examples/circle.c
# Output binary: ./bin/targets/circle
```

---

### Run the Debugger

The orchestrator requires root privileges because CRIU needs low-level kernel access to checkpoint and restore processes.

```bash
sudo bin/orchestrator <num_processes> <path-to-binary> <criu|dmtcp>
```

**Example with 2 processes using CRIU backend:**

```bash
sudo bin/orchestrator 2 ./bin/targets/circle criu
```

Once running, you will see output similar to:

```
rpc server listening on address: localhost:3490
executing ./bin/targets/circle as an mpi job with 2 processes
Starting the gui - /usr/bin/npm run start:open
starting websocket server for gui
1 - binary started, waiting for command
0 - binary started, waiting for command
1 - rpc server listening on address: localhost:3501
0 - rpc server listening on address: localhost:3500
```

Each MPI process has a dedicated debug agent listening on its own port.  
The web-based GUI is also started automatically and can be accessed through your browser.

---

### Accessing the Web GUI

When you run the orchestrator, the debugger automatically starts a web-based graphical user interface. The GUI communicates with the debugger backend via WebSocket on port **3496**.

If you are running on a local machine, the GUI should open automatically in your default browser.

If you are running on a **remote server** (e.g., SSH, virtual machine, cloud instance) and the browser does not open automatically, you can manually establish the WebSocket connection using `curl`:

```bash
curl -i -N \
  -H "Connection: Upgrade" \
  -H "Upgrade: websocket" \
  -H "Host: localhost:3496" \
  -H "Origin: http://localhost:3496" \
  -H "Sec-WebSocket-Key: dGhllHNhbXBsZSBub25jZQ==" \
  -H "Sec-WebSocket-Version: 13" \
  http://localhost:3496/
```

> **Note:** The `Sec-WebSocket-Key` value above is a static example key.  
> In practice, the WebSocket handshake will generate a unique key, but this command is sufficient to verify that the GUI server is running and listening on the expected port.

You can also access the GUI by forwarding the port via SSH:

```bash
ssh -L 3496:localhost:3496 user@your-server
```

Then open `http://localhost:3496` in your local browser.

---

### Example Programs

The `examples/` directory contains sample MPI programs to test with:

```bash
# Compile
bin/compiler examples/circle.c

# Run with 2 processes
sudo bin/orchestrator 2 ./bin/targets/circle criu
```

The `circle.c` program demonstrates a circular message-passing pattern between MPI processes — a good test case for reversible execution.

---

## Environment Variables

To avoid setting PATH variables on every session, add these lines to your `~/.bashrc` or `~/.profile`:

```bash
# GCC 8.5.0
export PATH=/usr/local/gcc-8.5.0/bin:$PATH

# Go 1.21
export PATH=/usr/local/go/bin:$PATH

# MPICH 3.4.3
export PATH=/usr/local/mpich-3.4.3/bin:$PATH

# Compiler flags for debugger-compatible MPICH compilation
export CC=/usr/local/gcc-8.5.0/bin/gcc
export CXX=/usr/local/gcc-8.5.0/bin/g++
```

Apply changes:

```bash
source ~/.bashrc
```

---

## Backends

This debugger supports two checkpointing backends.  
Details for both are documented in [manual.md](manual.md).

| Backend | Description                             | Status        |
|---------|-----------------------------------------|---------------|
| CRIU    | Checkpoint/Restore in Userspace         | ✅ Recommended |
| DMTCP   | Distributed MultiThreaded CheckPointing | ✅ Supported   |

---

## Notes

- Running as `sudo` is required due to CRIU's need for kernel-level access.
- If `mpirun` is not found when running with `sudo`, ensure the MPICH binary path is included in the **root user's** PATH as well:
  ```bash
  sudo env PATH=$PATH bin/orchestrator 2 ./bin/targets/circle criu
  ```