#!/usr/bin/env python3
"""Expose EC20F CoreAudio PCM through the encrypted remote-PCM framing protocol."""

from __future__ import annotations

import argparse
import math
import struct
import ssl
import socket
import subprocess
import threading
import time

MAGIC = b"SDP1"
CAPTURE = 0
PLAYBACK = 1
FRAME_BYTES = 320
SILENCE_PEAK_THRESHOLD = 32
SILENCE_RMS_THRESHOLD = 20


def read_exact(stream, length):
    data = bytearray()
    while len(data) < length:
        chunk = stream.read(length - len(data))
        if not chunk:
            raise RuntimeError("CoreAudio helper closed")
        data.extend(chunk)
    return bytes(data)


def read_remote_frame(connection):
    header = read_exact(connection, 9)
    if header[:4] != MAGIC or header[4] != PLAYBACK:
        raise RuntimeError("invalid remote PCM frame")
    length = int.from_bytes(header[5:], "big")
    if length <= 0 or length > 4096:
        raise RuntimeError("invalid remote PCM frame length")
    return read_exact(connection, length)


def write_remote_frame(connection, direction, payload):
    connection.sendall(MAGIC + bytes([direction]) + len(payload).to_bytes(4, "big") + payload)


class AudioMeter:
    def __init__(self, direction):
        self.direction = direction
        self.samples = 0
        self.squares = 0
        self.peak = 0
        self.report_at = time.monotonic() + 1

    def add_frame(self, frame):
        values = calculate_levels(frame)
        self.samples += len(values)
        self.squares += sum(value * value for value in values)
        if values:
            self.peak = max(self.peak, max(abs(value) for value in values))
        if time.monotonic() < self.report_at:
            return
        rms = math.sqrt(self.squares / self.samples) if self.samples else 0
        active = self.peak > SILENCE_PEAK_THRESHOLD and rms > SILENCE_RMS_THRESHOLD
        print(
            f"CALL_AUDIO direction={self.direction} bytes={self.samples * 2} "
            f"peak={self.peak} rms={rms:.1f} signal={active}",
            flush=True,
        )
        self.samples = 0
        self.squares = 0
        self.peak = 0
        self.report_at = time.monotonic() + 1


def calculate_levels(frame):
    if len(frame) % 2:
        raise ValueError("PCM16 frame must contain complete samples")
    return struct.unpack(f"<{len(frame) // 2}h", frame)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--listen", default="0.0.0.0")
    parser.add_argument("--port", type=int, default=19002)
    parser.add_argument("--cert", required=True)
    parser.add_argument("--key", required=True)
    parser.add_argument("--ca", required=True)
    parser.add_argument("--helper", default="/tmp/sim-deck-macos-coreaudio-pcm")
    args = parser.parse_args()

    tls_context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
    tls_context.minimum_version = ssl.TLSVersion.TLSv1_3
    tls_context.load_cert_chain(args.cert, args.key)
    tls_context.load_verify_locations(cafile=args.ca)
    tls_context.verify_mode = ssl.CERT_REQUIRED

    listener = socket.socket()
    listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    listener.bind((args.listen, args.port))
    listener.listen(1)
    print(f"CoreAudio PCM TLS bridge listening on {args.listen}:{args.port}", flush=True)
    raw_client, address = listener.accept()
    connection = tls_context.wrap_socket(raw_client, server_side=True)
    helper = subprocess.Popen([args.helper], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=None)
    print(f"TLS PCM client connected from {address[0]}:{address[1]}", flush=True)
    stopped = threading.Event()

    def capture():
        meter = AudioMeter("modem_to_app")
        try:
            while not stopped.is_set():
                frame = read_exact(helper.stdout, FRAME_BYTES)
                write_remote_frame(connection, CAPTURE, frame)
                meter.add_frame(frame)
        except (OSError, RuntimeError):
            stopped.set()

    def playback():
        meter = AudioMeter("app_to_modem")
        try:
            while not stopped.is_set():
                frame = read_remote_frame(connection)
                meter.add_frame(frame)
                helper.stdin.write(frame)
                helper.stdin.flush()
        except (OSError, RuntimeError):
            stopped.set()

    capture_thread = threading.Thread(target=capture, daemon=True)
    playback_thread = threading.Thread(target=playback, daemon=True)
    capture_thread.start()
    playback_thread.start()
    stopped.wait()
    helper.terminate()
    helper.wait(timeout=3)
    connection.close()
    listener.close()


if __name__ == "__main__":
    main()
