# Causally Consistent Reversible Debugger for MPI

A reversible debugger for MPI applications that allows stepping backwards through program execution using process
checkpointing.

> ⚠️ **Architecture Notice:** Only `x86_64` is currently supported.  
> When using the **CRIU** backend, root privileges are required; running inside a **container** or **virtual machine** is strongly recommended.
> The **DMTCP** backend operates entirely in user space.

---

## Table of Contents

- [Requirements](#requirements)
- [Building the Toolchain from Source](#building-the-toolchain-from-source)
    - [1. GCC 8.5.0](#1-gcc-850)
    - [2. MPICH 3.4.3](#2-mpich-343)
    - [3. Go 1.21](#3-go-121)
    - [4. CRIU 3.19 (Recommended Backend)](#4-criu-319-recommended-backend)
    - [5. DMTCP (Alternative Backend)](#5-dmtcp-alternative-backend)
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

This debugger has been tested with the following toolchain versions.
Using newer versions (e.g., GCC 13 on Ubuntu 24.04) will cause compatibility issues with the DWARF debug metadata
parser.

| Tool  | Version    | Why this version?                                                              |
|-------|------------|--------------------------------------------------------------------------------|
| GCC   | 8.5.0      | Must produce DWARF level 4 debug data                                          |
| MPICH | 3.4.3      | Tested MPI implementation                                                      |
| Go    | 1.21       | Required for building the debugger                                             |
| CRIU  | 3.19       | High-performance kernel-assisted checkpointing backend (Default / Recommended) |
| DMTCP | 2.6+ / 3.0 | User-space checkpointing backend (Alternative, runs without root)              |

> **Note for Ubuntu 24.04 users:**  
> Ubuntu 24.04 ships with GCC 13, Go 1.22+, and does not include CRIU in its default repositories.
> Follow the source-build instructions below for a clean environment.

---

## Building the Toolchain from Source

First, install the base dependencies needed for all builds:

```bash
sudo apt-get update
sudo apt-get install -y build-essential wget curl libgmp-dev libmpfr-dev \
    libmpc-dev flex bison pkg-config libprotobuf-dev libprotobuf-c-dev \
    protobuf-c-compiler protobuf-compiler python3-protobuf libnl-3-dev \
    libnet-dev libcap-dev bsdmainutils asciidoc npm git
```

---

### 1. GCC 8.5.0

The debugger's DWARF parser requires executables compiled with DWARF level 4.
GCC 8.5.0 produces this format by default, whereas GCC 11+ produces DWARF 5.

```bash
mkdir -p ~/toolchain/gcc-build && cd ~/toolchain/gcc-build

# Download source
wget https://ftpmirror.gnu.org/gcc/gcc-8.5.0/gcc-8.5.0.tar.gz
tar xzf gcc-8.5.0.tar.gz
cd gcc-8.5.0

# Download internal prerequisites
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

### 4. CRIU 3.19 (Recommended Backend)

CRIU is the checkpointing backend used to snapshot and restore MPI processes.  
It is not available in Ubuntu 24.04's official repositories and must be built from source using GCC 8.5.0.

```bash
cd ~/toolchain

# Download source
wget https://github.com/checkpoint-restore/criu/archive/refs/tags/v3.19.tar.gz
tar -xzf v3.19.tar.gz
cd criu-3.19

# Build using GCC 8.5.0
export CC=/usr/local/gcc-8.5.0/bin/gcc
export HOSTCC=/usr/local/gcc-8.5.0/bin/gcc

make
sudo make install
```

Verify installation:

```bash
criu --version
# Expected: Version: 3.19
```

---

### 5. DMTCP (Alternative Backend)

DMTCP (Distributed MultiThreaded Checkpointing) is supported as an alternative backend.
Unlike CRIU, DMTCP operates entirely in **user space** and does **not require root privileges**, making it ideal for restricted HPC clusters.

#### Option A: Install via Package Manager (Ubuntu/Debian)

```bash
sudo apt-get install -y dmtcp
```

#### Option B: Build from Source

```bash
cd ~/toolchain

git clone https://github.com/dmtcp/dmtcp.git
cd dmtcp
./configure --prefix=/usr/local/dmtcp
make -j$(nproc)
sudo make install
```

Add DMTCP to your PATH:

```bash
export PATH=/usr/local/dmtcp/bin:$PATH
```

Verify installation:

```bash
dmtcp_launch --version
# or
dmtcp_coordinator --version
```

---

## Building the Debugger

Once all toolchain components are installed, clone the repository and build:

```bash
git clone https://github.com/mihkeltiks/rev-mpi-deb.git
cd rev-mpi-deb

make
```

This compiles the following binaries into `./bin/`:

| Binary              | Description                   |
|---------------------|-------------------------------|
| `bin/orchestrator`  | Main debugger process manager |
| `bin/compiler`      | MPI wrapper compiler          |
| `bin/node-debugger` | Per-process debug agent       |

---

## Usage

### Compile Your Program

Programs must be compiled with the included compiler wrapper to instrument MPI calls:

```bash
bin/compiler <path-to-your-mpi-program>
```

The compiled binary will be placed in `./bin/targets/<source-file-name>`.

**Example:**

```bash
bin/compiler examples/circle.c
# Output binary: ./bin/targets/circle
```

---

### Run the Debugger

The orchestrator syntax is:

```bash
bin/orchestrator <num_processes> <path-to-binary> <criu|dmtcp>
```

#### Running with CRIU (Requires Root):

Because CRIU needs kernel-level access to inspect and restore processes, run with `sudo`:

```bash
sudo env PATH=$PATH bin/orchestrator 2 ./bin/targets/circle criu
```

#### Running with DMTCP (User Space):

DMTCP does not require root permissions:

```bash
bin/orchestrator 2 ./bin/targets/circle dmtcp
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

When the orchestrator starts, it launches a web-based GUI communicating via WebSocket on port **3496**.
If you are running on a local machine, the GUI will open automatically in your default browser.
If you are running on a **remote server** (e.g., SSH, cloud VM) without a desktop environment:

1. Forward the port to your local machine:

```bash
ssh -L 3496:localhost:3496 user@your-server
```

2. Open `http://localhost:3496` in your local browser.

You can also verify that the WebSocket listener is active using `curl`:

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

---

### Example Programs

The `examples/` directory contains sample MPI programs:

```bash
# Compile sample
bin/compiler examples/circle.c

# Run with CRIU
sudo env PATH=$PATH bin/orchestrator 2 ./bin/targets/circle criu

# Or run with DMTCP
bin/orchestrator 2 ./bin/targets/circle dmtcp
```

The `circle.c` program demonstrates a circular message-passing pattern between MPI processes — a good test case for reversible execution.

---

## Environment Variables

To avoid re-exporting paths in every shell session, add the following to `~/.bashrc`:

```bash
# GCC 8.5.0
export PATH=/usr/local/gcc-8.5.0/bin:$PATH

# Go 1.21
export PATH=/usr/local/go/bin:$PATH

# MPICH 3.4.3
export PATH=/usr/local/mpich-3.4.3/bin:$PATH

# DMTCP (if built from source)
export PATH=/usr/local/dmtcp/bin:$PATH

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

This debugger provides two interchangeable checkpointing backends.
See [manual.md](manual.md) for deeper implementation details.

| Backend   | Mechanism                            | Root Required?              | Advantages / Use Cases                                                                                                       |
|-----------|--------------------------------------|-----------------------------|------------------------------------------------------------------------------------------------------------------------------|
| **CRIU**  | Kernel ptrace & userspace dump       | Yes (`sudo` / capabilities) | **Recommended**: Faster checkpoint/restore cycles, lower runtime overhead, and supports incremental checkpointing.           |
| **DMTCP** | Library injection & user coordinator | No                          | **Flexible**: Runs unprivileged in userspace; ideal for environments where root or container capabilities cannot be granted. |

---

## Notes

* **PATH with `sudo`:** When running with `sudo` under CRIU, default environment settings might strip `/usr/local/bin`from root's `PATH`.
Always use `sudo env PATH=$PATH bin/orchestrator ...` to ensure `mpirun` and `criu` binaries are reachable.
* **Port Allocation:** Ensure ports `3490`, `3496`, and `3500+` are not blocked or bound by other services.