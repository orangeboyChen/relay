#!/usr/bin/env python3
"""Bridge an EC20F USB AT bulk interface to one mutual-TLS TCP client."""

from __future__ import annotations

import argparse
import ctypes
import ctypes.util
import signal
import socket
import ssl
import sys
import threading
import time

VENDOR_ID = 0x2C7C
PRODUCT_ID = 0x0125
INTERFACE = 2
IN_ENDPOINT = 0x84
OUT_ENDPOINT = 0x03
BULK_TIMEOUT_MS = 1000
# The device agent's default AT command timeout is five seconds. Returning a
# terminal error first keeps a USB exchange timeout from leaving the agent's
# pending command attached to a response that belongs to the next command.
EXCHANGE_TIMEOUT_SECONDS = 4.0
STALE_RESPONSE_DRAIN_SECONDS = 0.25


def load_libusb():
    candidates = [ctypes.util.find_library("usb-1.0"), "/opt/homebrew/opt/libusb/lib/libusb-1.0.dylib", "/usr/local/opt/libusb/lib/libusb-1.0.dylib"]
    for candidate in candidates:
        if candidate:
            try:
                return ctypes.CDLL(candidate)
            except OSError:
                pass
    raise RuntimeError("libusb-1.0 was not found; install it with Homebrew")


def configure_api(usb):
    usb.libusb_init.argtypes = [ctypes.POINTER(ctypes.c_void_p)]
    usb.libusb_init.restype = ctypes.c_int
    usb.libusb_exit.argtypes = [ctypes.c_void_p]
    usb.libusb_get_device_list.argtypes = [ctypes.c_void_p, ctypes.POINTER(ctypes.POINTER(ctypes.c_void_p))]
    usb.libusb_get_device_list.restype = ctypes.c_ssize_t
    usb.libusb_free_device_list.argtypes = [ctypes.POINTER(ctypes.c_void_p), ctypes.c_int]
    usb.libusb_get_device_descriptor.argtypes = [ctypes.c_void_p, ctypes.c_void_p]
    usb.libusb_get_device_descriptor.restype = ctypes.c_int
    usb.libusb_open.argtypes = [ctypes.c_void_p, ctypes.POINTER(ctypes.c_void_p)]
    usb.libusb_open.restype = ctypes.c_int
    usb.libusb_close.argtypes = [ctypes.c_void_p]
    usb.libusb_claim_interface.argtypes = [ctypes.c_void_p, ctypes.c_int]
    usb.libusb_claim_interface.restype = ctypes.c_int
    usb.libusb_release_interface.argtypes = [ctypes.c_void_p, ctypes.c_int]
    usb.libusb_bulk_transfer.argtypes = [ctypes.c_void_p, ctypes.c_ubyte, ctypes.POINTER(ctypes.c_ubyte), ctypes.c_int, ctypes.POINTER(ctypes.c_int), ctypes.c_uint]
    usb.libusb_bulk_transfer.restype = ctypes.c_int
    usb.libusb_error_name.argtypes = [ctypes.c_int]
    usb.libusb_error_name.restype = ctypes.c_char_p


class DeviceDescriptor(ctypes.Structure):
    _fields_ = [("bLength", ctypes.c_uint8), ("bDescriptorType", ctypes.c_uint8), ("bcdUSB", ctypes.c_uint16), ("bDeviceClass", ctypes.c_uint8), ("bDeviceSubClass", ctypes.c_uint8), ("bDeviceProtocol", ctypes.c_uint8), ("bMaxPacketSize0", ctypes.c_uint8), ("idVendor", ctypes.c_uint16), ("idProduct", ctypes.c_uint16)]


def error_name(usb, code):
    return usb.libusb_error_name(code).decode("ascii", errors="replace")


def find_device(usb, context):
    devices = ctypes.POINTER(ctypes.c_void_p)()
    count = usb.libusb_get_device_list(context, ctypes.byref(devices))
    if count < 0:
        raise RuntimeError(f"device list: {error_name(usb, count)}")
    descriptor = DeviceDescriptor()
    for index in range(count):
        if usb.libusb_get_device_descriptor(devices[index], ctypes.byref(descriptor)) == 0 and descriptor.idVendor == VENDOR_ID and descriptor.idProduct == PRODUCT_ID:
            return devices, devices[index]
    usb.libusb_free_device_list(devices, 1)
    raise RuntimeError(f"Quectel {VENDOR_ID:04x}:{PRODUCT_ID:04x} not found")


def describe_exchange(payload):
    text = payload.decode("ascii", errors="replace").strip()
    if not text.startswith("AT"):
        return f"binary payload ({len(payload)} bytes)"
    first_line = text.splitlines()[0]
    if first_line.startswith("ATD"):
        return "ATD [redacted]"
    if first_line.startswith("AT+"):
        end = 3
        while end < len(first_line) and (first_line[end].isalnum() or first_line[end] == "_"):
            end += 1
        return first_line[:end]
    return first_line[:16]


def describe_response(response):
    for marker in (b"+CME ERROR:", b"+CMS ERROR:", b"NO CARRIER", b"ERROR", b"OK", b"> "):
        if marker in response:
            return marker.decode("ascii").strip()
    return "partial"


def exchange(usb, handle, payload):
    command = describe_exchange(payload)
    print(f"AT_TX {command}", flush=True)
    deadline = time.monotonic() + EXCHANGE_TIMEOUT_SECONDS
    request = (ctypes.c_ubyte * len(payload)).from_buffer_copy(payload)
    transferred = ctypes.c_int()
    result = usb.libusb_bulk_transfer(handle, OUT_ENDPOINT, request, len(payload), ctypes.byref(transferred), BULK_TIMEOUT_MS)
    if result != 0:
        raise RuntimeError(f"USB write: {error_name(usb, result)}")
    response = bytearray()
    while time.monotonic() < deadline:
        buffer = (ctypes.c_ubyte * 4096)()
        result = usb.libusb_bulk_transfer(handle, IN_ENDPOINT, buffer, len(buffer), ctypes.byref(transferred), BULK_TIMEOUT_MS)
        if result == -7:
            continue
        if result != 0:
            raise RuntimeError(f"USB read: {error_name(usb, result)}")
        response.extend(bytes(buffer[:transferred.value]))
        if b"\r\n> " in response or b"\r\nOK\r\n" in response or b"\r\nERROR\r\n" in response or b"NO CARRIER" in response or b"+CME ERROR:" in response or b"+CMS ERROR:" in response:
            result = bytes(response)
            print(f"AT_RX {describe_response(result)} ({len(result)} bytes)", flush=True)
            return result
    print(f"AT_RX_TIMEOUT {command}", flush=True)
    raise RuntimeError(f"USB response timeout after {EXCHANGE_TIMEOUT_SECONDS:.1f}s")


def drain_stale_responses(usb, handle):
    """Discard bytes left by a timed-out USB exchange before the next command."""
    deadline = time.monotonic() + STALE_RESPONSE_DRAIN_SECONDS
    while time.monotonic() < deadline:
        buffer = (ctypes.c_ubyte * 4096)()
        transferred = ctypes.c_int()
        result = usb.libusb_bulk_transfer(
            handle,
            IN_ENDPOINT,
            buffer,
            len(buffer),
            ctypes.byref(transferred),
            20,
        )
        if result == -7:
            return
        if result != 0:
            raise RuntimeError(f"USB stale-response drain: {error_name(usb, result)}")


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--listen", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=19001)
    parser.add_argument("--cert", required=True)
    parser.add_argument("--key", required=True)
    parser.add_argument("--ca", required=True)
    args = parser.parse_args()

    usb = load_libusb()
    configure_api(usb)
    context = ctypes.c_void_p()
    result = usb.libusb_init(ctypes.byref(context))
    if result != 0:
        raise RuntimeError(f"libusb_init: {error_name(usb, result)}")
    devices = handle = None
    stopped = threading.Event()
    listener = client = None
    needs_drain = False
    try:
        devices, device = find_device(usb, context)
        handle = ctypes.c_void_p()
        result = usb.libusb_open(device, ctypes.byref(handle))
        if result != 0:
            raise RuntimeError(f"open: {error_name(usb, result)}")
        result = usb.libusb_claim_interface(handle, INTERFACE)
        if result != 0:
            raise RuntimeError(f"claim AT interface {INTERFACE}: {error_name(usb, result)}")

        tls_context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        tls_context.minimum_version = ssl.TLSVersion.TLSv1_3
        tls_context.load_cert_chain(args.cert, args.key)
        tls_context.load_verify_locations(cafile=args.ca)
        tls_context.verify_mode = ssl.CERT_REQUIRED
        listener = socket.socket()
        listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        listener.bind((args.listen, args.port))
        listener.listen(1)
        listener.settimeout(1)
        signal.signal(signal.SIGINT, lambda *_: stopped.set())
        signal.signal(signal.SIGTERM, lambda *_: stopped.set())
        print(f"EC20F AT TLS bridge listening on {args.listen}:{args.port}; USB interface {INTERFACE}, IN=0x{IN_ENDPOINT:02x}, OUT=0x{OUT_ENDPOINT:02x}", flush=True)
        while not stopped.is_set():
            try:
                raw_client, address = listener.accept()
            except socket.timeout:
                continue
            try:
                client = tls_context.wrap_socket(raw_client, server_side=True)
            except ssl.SSLError as exc:
                print(f"TLS client rejected: {exc}", file=sys.stderr, flush=True)
                raw_client.close()
                continue
            client.settimeout(1)
            print(f"TLS AT client connected from {address[0]}:{address[1]}", flush=True)
            try:
                while not stopped.is_set():
                    try:
                        payload = client.recv(4096)
                    except socket.timeout:
                        continue
                    if not payload:
                        break
                    try:
                        if needs_drain:
                            drain_stale_responses(usb, handle)
                            needs_drain = False
                        response = exchange(usb, handle, payload)
                    except RuntimeError as exc:
                        print(f"USB exchange failed: {exc}", file=sys.stderr, flush=True)
                        needs_drain = True
                        response = b"\r\n+CME ERROR: 515\r\n"
                    try:
                        client.sendall(response)
                    except (BrokenPipeError, ConnectionResetError, ssl.SSLError):
                        break
            finally:
                client.close()
                client = None
        return 0
    finally:
        stopped.set()
        if client:
            client.close()
        if listener:
            listener.close()
        if handle:
            usb.libusb_release_interface(handle, INTERFACE)
            usb.libusb_close(handle)
        if devices:
            usb.libusb_free_device_list(devices, 1)
        usb.libusb_exit(context)


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        raise SystemExit(130)
    except Exception as exc:
        print(f"bridge failed: {exc}", file=sys.stderr)
        raise SystemExit(1)
