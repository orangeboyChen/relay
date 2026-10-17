import importlib.util
import pathlib
import unittest


MODULE_PATH = pathlib.Path(__file__).with_name("macos-at-bridge.py")
SPEC = importlib.util.spec_from_file_location("macos_at_bridge", MODULE_PATH)
BRIDGE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(BRIDGE)


class FakeUSB:
    def __init__(self, results):
        self.results = iter(results)
        self.calls = 0

    def libusb_bulk_transfer(self, *_args):
        self.calls += 1
        return next(self.results)


class StaleResponseDrainTests(unittest.TestCase):
    def test_drains_available_bytes_until_endpoint_is_quiet(self):
        usb = FakeUSB([0, -7])

        BRIDGE.drain_stale_responses(usb, object())

        self.assertEqual(usb.calls, 2)

    def test_does_not_wait_for_a_full_frame_when_endpoint_is_empty(self):
        usb = FakeUSB([-7])

        BRIDGE.drain_stale_responses(usb, object())

        self.assertEqual(usb.calls, 1)


if __name__ == "__main__":
    unittest.main()
