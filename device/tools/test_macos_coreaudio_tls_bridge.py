import contextlib
import importlib.util
import io
import pathlib
import struct
import unittest
from unittest import mock


MODULE_PATH = pathlib.Path(__file__).with_name("macos-coreaudio-tls-bridge.py")
SPEC = importlib.util.spec_from_file_location("macos_coreaudio_tls_bridge", MODULE_PATH)
BRIDGE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(BRIDGE)


class AudioMeterTests(unittest.TestCase):
    def test_reports_active_pcm_with_direction_and_levels(self):
        output = io.StringIO()
        with mock.patch.object(BRIDGE.time, "monotonic", side_effect=[0, 2, 3]):
            meter = BRIDGE.AudioMeter("app_to_modem")
            with contextlib.redirect_stdout(output):
                meter.add_frame(struct.pack("<4h", 100, -100, 100, -100))

        self.assertIn("direction=app_to_modem", output.getvalue())
        self.assertIn("bytes=8 peak=100 rms=100.0 signal=True", output.getvalue())

    def test_reports_silence_as_inactive(self):
        output = io.StringIO()
        with mock.patch.object(BRIDGE.time, "monotonic", side_effect=[0, 2, 3]):
            meter = BRIDGE.AudioMeter("modem_to_app")
            with contextlib.redirect_stdout(output):
                meter.add_frame(bytes(BRIDGE.FRAME_BYTES))

        self.assertIn("direction=modem_to_app", output.getvalue())
        self.assertIn("peak=0 rms=0.0 signal=False", output.getvalue())

    def test_rejects_partial_pcm16_sample(self):
        with self.assertRaisesRegex(ValueError, "complete samples"):
            BRIDGE.calculate_levels(b"\x00")


if __name__ == "__main__":
    unittest.main()
