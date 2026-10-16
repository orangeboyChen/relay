#!/usr/bin/env python3
"""Bridge EC20F USB Audio Class isochronous PCM to the device container."""

from __future__ import annotations

import argparse
import ctypes
import ctypes.util
import queue
import signal
import socket
import ssl
import struct
import sys
import threading

VENDOR_ID = 0x2C7C
PRODUCT_ID = 0x0125
CAPTURE_INTERFACE = 7
PLAYBACK_INTERFACE = 8
CAPTURE_ENDPOINT = 0x8A
PLAYBACK_ENDPOINT = 0x06
ALTERNATE_SETTING = 1
MAGIC = b"SDP1"
CAPTURE_DIRECTION = 0
PLAYBACK_DIRECTION = 1
PCM_TRANSFER_BYTES = 320
MAX_FRAME_BYTES = 4096


class DeviceDescriptor(ctypes.Structure):
    _fields_ = [("bLength", ctypes.c_uint8), ("bDescriptorType", ctypes.c_uint8), ("bcdUSB", ctypes.c_uint16), ("bDeviceClass", ctypes.c_uint8), ("bDeviceSubClass", ctypes.c_uint8), ("bDeviceProtocol", ctypes.c_uint8), ("bMaxPacketSize0", ctypes.c_uint8), ("idVendor", ctypes.c_uint16), ("idProduct", ctypes.c_uint16), ("bcdDevice", ctypes.c_uint16), ("iManufacturer", ctypes.c_uint8), ("iProduct", ctypes.c_uint8), ("iSerialNumber", ctypes.c_uint8), ("bNumConfigurations", ctypes.c_uint8)]


class EndpointDescriptor(ctypes.Structure):
    _fields_ = [("bLength", ctypes.c_uint8), ("bDescriptorType", ctypes.c_uint8), ("bEndpointAddress", ctypes.c_uint8), ("bmAttributes", ctypes.c_uint8), ("wMaxPacketSize", ctypes.c_uint16), ("bInterval", ctypes.c_uint8), ("bRefresh", ctypes.c_uint8), ("bSynchAddress", ctypes.c_uint8), ("extra", ctypes.POINTER(ctypes.c_ubyte)), ("extra_length", ctypes.c_int)]


class InterfaceDescriptor(ctypes.Structure):
    _fields_ = [("bLength", ctypes.c_uint8), ("bDescriptorType", ctypes.c_uint8), ("bInterfaceNumber", ctypes.c_uint8), ("bAlternateSetting", ctypes.c_uint8), ("bNumEndpoints", ctypes.c_uint8), ("bInterfaceClass", ctypes.c_uint8), ("bInterfaceSubClass", ctypes.c_uint8), ("bInterfaceProtocol", ctypes.c_uint8), ("iInterface", ctypes.c_uint8), ("endpoint", ctypes.POINTER(EndpointDescriptor)), ("extra", ctypes.POINTER(ctypes.c_ubyte)), ("extra_length", ctypes.c_int)]


class Interface(ctypes.Structure):
    _fields_ = [("altsetting", ctypes.POINTER(InterfaceDescriptor)), ("num_altsetting", ctypes.c_int)]


class ConfigDescriptor(ctypes.Structure):
    _fields_ = [("bLength", ctypes.c_uint8), ("bDescriptorType", ctypes.c_uint8), ("wTotalLength", ctypes.c_uint16), ("bNumInterfaces", ctypes.c_uint8), ("bConfigurationValue", ctypes.c_uint8), ("iConfiguration", ctypes.c_uint8), ("bmAttributes", ctypes.c_uint8), ("MaxPower", ctypes.c_uint8), ("interface", ctypes.POINTER(Interface)), ("extra", ctypes.POINTER(ctypes.c_ubyte)), ("extra_length", ctypes.c_int)]


class Transfer(ctypes.Structure):
    pass


class IsoPacketDescriptor(ctypes.Structure):
    _fields_ = [("length", ctypes.c_uint), ("actual_length", ctypes.c_uint), ("status", ctypes.c_int), ("offset", ctypes.c_uint)]


CALLBACK = ctypes.CFUNCTYPE(None, ctypes.POINTER(Transfer))
Transfer._fields_ = [("dev_handle", ctypes.c_void_p), ("flags", ctypes.c_ubyte), ("endpoint", ctypes.c_ubyte), ("type", ctypes.c_ubyte), ("timeout", ctypes.c_uint), ("status", ctypes.c_int), ("length", ctypes.c_int), ("actual_length", ctypes.c_int), ("callback", CALLBACK), ("user_data", ctypes.c_void_p), ("buffer", ctypes.POINTER(ctypes.c_ubyte)), ("num_iso_packets", ctypes.c_int)]


def iso_packet_descriptors(transfer):
    address = ctypes.addressof(transfer.contents) + ctypes.sizeof(Transfer)
    return ctypes.cast(address, ctypes.POINTER(IsoPacketDescriptor))


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
    usb.libusb_get_device_descriptor.argtypes = [ctypes.c_void_p, ctypes.POINTER(DeviceDescriptor)]
    usb.libusb_get_device_descriptor.restype = ctypes.c_int
    usb.libusb_get_active_config_descriptor.argtypes = [ctypes.c_void_p, ctypes.POINTER(ctypes.POINTER(ConfigDescriptor))]
    usb.libusb_get_active_config_descriptor.restype = ctypes.c_int
    usb.libusb_free_config_descriptor.argtypes = [ctypes.POINTER(ConfigDescriptor)]
    usb.libusb_open.argtypes = [ctypes.c_void_p, ctypes.POINTER(ctypes.c_void_p)]
    usb.libusb_open.restype = ctypes.c_int
    usb.libusb_close.argtypes = [ctypes.c_void_p]
    usb.libusb_claim_interface.argtypes = [ctypes.c_void_p, ctypes.c_int]
    usb.libusb_claim_interface.restype = ctypes.c_int
    usb.libusb_release_interface.argtypes = [ctypes.c_void_p, ctypes.c_int]
    usb.libusb_release_interface.restype = ctypes.c_int
    usb.libusb_set_interface_alt_setting.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_int]
    usb.libusb_set_interface_alt_setting.restype = ctypes.c_int
    usb.libusb_alloc_transfer.argtypes = [ctypes.c_int]
    usb.libusb_alloc_transfer.restype = ctypes.POINTER(Transfer)
    usb.libusb_free_transfer.argtypes = [ctypes.POINTER(Transfer)]
    usb.libusb_submit_transfer.argtypes = [ctypes.POINTER(Transfer)]
    usb.libusb_submit_transfer.restype = ctypes.c_int
    usb.libusb_cancel_transfer.argtypes = [ctypes.POINTER(Transfer)]
    usb.libusb_handle_events_timeout_completed.argtypes = [ctypes.c_void_p, ctypes.c_void_p, ctypes.POINTER(ctypes.c_int)]
    usb.libusb_handle_events_timeout_completed.restype = ctypes.c_int
    usb.libusb_error_name.argtypes = [ctypes.c_int]
    usb.libusb_error_name.restype = ctypes.c_char_p


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


def endpoint_packet_size(config, interface_number, endpoint_address):
    for interface_index in range(config.bNumInterfaces):
        interface = config.interface[interface_index]
        for index in range(interface.num_altsetting):
            descriptor = interface.altsetting[index]
            if descriptor.bInterfaceNumber != interface_number or descriptor.bAlternateSetting != ALTERNATE_SETTING:
                continue
            for endpoint_index in range(descriptor.bNumEndpoints):
                endpoint = descriptor.endpoint[endpoint_index]
                if endpoint.bEndpointAddress == endpoint_address and endpoint.bmAttributes & 0x03 == 0x01:
                    packet_size = endpoint.wMaxPacketSize & 0x07FF
                    if packet_size == 0 or PCM_TRANSFER_BYTES % packet_size != 0:
                        raise RuntimeError(f"endpoint 0x{endpoint_address:02x} has unsupported packet size {packet_size}")
                    return packet_size
    raise RuntimeError(f"interface {interface_number} has no isochronous endpoint 0x{endpoint_address:02x} on alternate setting {ALTERNATE_SETTING}")


class IsoStream:
    def __init__(self, usb, handle, endpoint, direction, packet_size, stopped):
        self.usb = usb
        self.handle = handle
        self.endpoint = endpoint
        self.direction = direction
        self.packet_size = packet_size
        self.packets_per_transfer = PCM_TRANSFER_BYTES // packet_size
        self.stopped = stopped
        self.frames = queue.Queue(maxsize=128)
        self.transfers = []
        self.buffers = []
        self.transfer_addresses = {}
        self.callback = CALLBACK(self._complete)
        self.error = None
        self.playback_pending = bytearray()

    def start(self):
        for _ in range(4):
            transfer = self.usb.libusb_alloc_transfer(self.packets_per_transfer)
            if not transfer:
                raise RuntimeError("libusb_alloc_transfer failed")
            buffer = (ctypes.c_ubyte * PCM_TRANSFER_BYTES)()
            descriptors = iso_packet_descriptors(transfer)
            self.transfers.append(transfer)
            self.buffers.append(buffer)
            self.transfer_addresses[ctypes.addressof(transfer.contents)] = len(self.transfers) - 1
            transfer.contents.dev_handle = self.handle
            transfer.contents.endpoint = self.endpoint
            transfer.contents.type = 1
            transfer.contents.timeout = 0
            transfer.contents.length = ctypes.sizeof(buffer)
            transfer.contents.callback = self.callback
            transfer.contents.user_data = None
            transfer.contents.buffer = ctypes.cast(buffer, ctypes.POINTER(ctypes.c_ubyte))
            transfer.contents.num_iso_packets = self.packets_per_transfer
            for packet in range(self.packets_per_transfer):
                descriptors[packet].length = self.packet_size
                descriptors[packet].offset = packet * self.packet_size
            result = self.usb.libusb_submit_transfer(transfer)
            if result != 0:
                raise RuntimeError(f"submit isochronous transfer: {error_name(self.usb, result)}")

    def _complete(self, transfer_pointer):
        transfer = transfer_pointer.contents
        if self.stopped.is_set():
            return
        if transfer.status != 0:
            self.error = RuntimeError(f"isochronous transfer failed with status {transfer.status}")
            self.stopped.set()
            return
        if self.direction == CAPTURE_DIRECTION:
            index = self.transfer_addresses[ctypes.addressof(transfer_pointer.contents)]
            descriptors = iso_packet_descriptors(transfer_pointer)
            payload = b"".join(bytes(self.buffers[index][descriptors[packet].offset:descriptors[packet].offset + descriptors[packet].actual_length]) for packet in range(self.packets_per_transfer) if descriptors[packet].status == 0)
            if payload:
                try:
                    self.frames.put_nowait(payload)
                except queue.Full:
                    self.error = RuntimeError("capture PCM queue is full")
                    self.stopped.set()
                    return
        else:
            try:
                while len(self.playback_pending) < PCM_TRANSFER_BYTES:
                    try:
                        self.playback_pending.extend(self.frames.get_nowait())
                    except queue.Empty:
                        break
                payload = bytes(self.playback_pending[:PCM_TRANSFER_BYTES])
                del self.playback_pending[:len(payload)]
            except queue.Empty:
                payload = b""
            index = self.transfer_addresses[ctypes.addressof(transfer_pointer.contents)]
            ctypes.memset(self.buffers[index], 0, PCM_TRANSFER_BYTES)
            ctypes.memmove(self.buffers[index], payload, len(payload))
        if not self.stopped.is_set() and self.usb.libusb_submit_transfer(transfer_pointer) != 0:
            self.error = RuntimeError("resubmitting isochronous transfer failed")
            self.stopped.set()

    def stop(self):
        for transfer in self.transfers:
            self.usb.libusb_cancel_transfer(transfer)
        for transfer in self.transfers:
            self.usb.libusb_free_transfer(transfer)


def write_frame(connection, direction, payload):
    if not payload:
        return
    connection.sendall(MAGIC + bytes([direction]) + struct.pack(">I", len(payload)) + payload)


def read_frame(connection):
    header = connection.recv(9, socket.MSG_WAITALL)
    if len(header) != 9 or header[:4] != MAGIC or header[4] != PLAYBACK_DIRECTION:
        raise RuntimeError("invalid remote PCM frame")
    length = struct.unpack(">I", header[5:])[0]
    if not 0 < length <= MAX_FRAME_BYTES:
        raise RuntimeError("invalid remote PCM frame length")
    payload = connection.recv(length, socket.MSG_WAITALL)
    if len(payload) != length:
        raise RuntimeError("truncated remote PCM frame")
    return payload


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--listen", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=19002)
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
    devices = None
    config_pointer = ctypes.POINTER(ConfigDescriptor)()
    handle = ctypes.c_void_p()
    claimed = []
    stopped = threading.Event()
    capture = playback = None
    listener = client = None
    try:
        devices, device = find_device(usb, context)
        config_pointer = ctypes.POINTER(ConfigDescriptor)()
        result = usb.libusb_get_active_config_descriptor(device, ctypes.byref(config_pointer))
        if result != 0:
            raise RuntimeError(f"configuration: {error_name(usb, result)}")
        capture_packet_size = endpoint_packet_size(config_pointer.contents, CAPTURE_INTERFACE, CAPTURE_ENDPOINT)
        playback_packet_size = endpoint_packet_size(config_pointer.contents, PLAYBACK_INTERFACE, PLAYBACK_ENDPOINT)
        result = usb.libusb_open(device, ctypes.byref(handle))
        if result != 0:
            raise RuntimeError(f"open: {error_name(usb, result)}")
        for interface in (CAPTURE_INTERFACE, PLAYBACK_INTERFACE):
            result = usb.libusb_claim_interface(handle, interface)
            if result != 0:
                raise RuntimeError(f"claim audio interface {interface}: {error_name(usb, result)}")
            claimed.append(interface)
            result = usb.libusb_set_interface_alt_setting(handle, interface, ALTERNATE_SETTING)
            if result != 0:
                raise RuntimeError(f"set audio interface {interface} alternate setting: {error_name(usb, result)}")
        tls_context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        tls_context.minimum_version = ssl.TLSVersion.TLSv1_3
        tls_context.load_cert_chain(args.cert, args.key)
        tls_context.load_verify_locations(cafile=args.ca)
        tls_context.verify_mode = ssl.CERT_REQUIRED
        listener = socket.socket()
        listener.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        listener.bind((args.listen, args.port))
        listener.listen(1)
        print(f"EC20F UAC TLS bridge listening on {args.listen}:{args.port}; capture=0x{CAPTURE_ENDPOINT:02x}, playback=0x{PLAYBACK_ENDPOINT:02x}", flush=True)
        signal.signal(signal.SIGINT, lambda *_: stopped.set())
        signal.signal(signal.SIGTERM, lambda *_: stopped.set())
        raw_client, address = listener.accept()
        client = tls_context.wrap_socket(raw_client, server_side=True)
        client.settimeout(1)
        print(f"TLS PCM client connected from {address[0]}:{address[1]}", flush=True)
        capture = IsoStream(usb, handle, CAPTURE_ENDPOINT, CAPTURE_DIRECTION, capture_packet_size, stopped)
        playback = IsoStream(usb, handle, PLAYBACK_ENDPOINT, PLAYBACK_DIRECTION, playback_packet_size, stopped)
        capture.start()
        playback.start()

        def events():
            timeout = ctypes.c_long(0)
            while not stopped.is_set():
                usb.libusb_handle_events_timeout_completed(context, ctypes.byref(timeout), None)

        event_thread = threading.Thread(target=events, daemon=True)
        event_thread.start()

        def send_capture():
            while not stopped.is_set():
                try:
                    write_frame(client, CAPTURE_DIRECTION, capture.frames.get(timeout=0.5))
                except queue.Empty:
                    continue
                except OSError:
                    stopped.set()

        def receive_playback():
            while not stopped.is_set():
                try:
                    playback.frames.put(read_frame(client), timeout=1)
                except (queue.Full, socket.timeout):
                    continue
                except (OSError, RuntimeError):
                    stopped.set()

        sender = threading.Thread(target=send_capture, daemon=True)
        receiver = threading.Thread(target=receive_playback, daemon=True)
        sender.start()
        receiver.start()
        while not stopped.wait(0.25):
            if capture.error or playback.error:
                raise capture.error or playback.error
        return 0
    finally:
        stopped.set()
        if capture:
            capture.stop()
        if playback:
            playback.stop()
        if client:
            client.close()
        if listener:
            listener.close()
        for interface in reversed(claimed):
            usb.libusb_release_interface(handle, interface)
        if handle:
            usb.libusb_close(handle)
        if config_pointer:
            usb.libusb_free_config_descriptor(config_pointer)
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
