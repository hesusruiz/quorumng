# We use a stock Go container to build Quorum Geth.
# The executables will be in /go-ethereum/build/bin/ so you need to map that directory
# when running this container if you want to get access to the resulting executable.
# You can build geth in the current directory like this:
# Rebuild the image when you have modified the sources.
#   docker build -t alabuilder .
# Run the container after building to get the executable
#   docker run --rm alabuilder >geth
#   chmod +x geth

# Use a stock Go builder container
FROM golang:1.19 as builder

# Get dependencies - will also be cached if we won't change go.mod/go.sum
COPY go.mod /go-ethereum/
COPY go.sum /go-ethereum/
WORKDIR /go-ethereum
RUN cd /go-ethereum && go mod tidy

# Add sources to the build directory inside container
ADD . /go-ethereum

# And build geth and newnodekey
RUN cd /go-ethereum && make geth newnodekey

# For runtime use a distroless image to make it as small as possible.
# There is an even smaller one ('gcr.io/distroless/static-debian11') which
# does not include glibc, libssl and openssl.
# But to play safe we use an image including those libraries.
FROM gcr.io/distroless/base-debian11

# Copy the application binaries
COPY --from=builder /go-ethereum/build/bin/geth /geth
COPY --from=builder /go-ethereum/build/bin/newnodekey /newnodekey

# Copy the cat command to help extract the binary from the container
COPY --from=builder /bin/cat /cat

# Make sure we are in the root directory
WORKDIR /
ENV PATH=/

# Expose the P2P port, both for TCP and UDP
EXPOSE 21000/tcp
EXPOSE 21000/udp

# Expose the JSON-RPC port for HTTP
EXPOSE 22000
# Expose the JSON-RPC port for WebSockets
EXPOSE 22001

CMD ["geth"]
