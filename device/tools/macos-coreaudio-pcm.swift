import CoreAudio
import Dispatch
import Foundation

private let frameSamples = 160
private let frameBytes = frameSamples * MemoryLayout<Int16>.size

private final class PCMBuffer {
    private let lock = NSLock()
    private var captured = Data()
    private var playback: [Float] = []

    func appendCapture(_ samples: UnsafeBufferPointer<Float>) {
        lock.lock()
        for sample in samples {
            let clipped = max(-1, min(1, sample))
            var pcm = Int16((clipped * Float(Int16.max)).rounded()).littleEndian
            withUnsafeBytes(of: &pcm) { captured.append(contentsOf: $0) }
        }
        lock.unlock()
    }

    func nextCaptureFrame() -> Data? {
        lock.lock()
        defer { lock.unlock() }
        guard captured.count >= frameBytes else { return nil }
        let frame = captured.prefix(frameBytes)
        captured.removeFirst(frameBytes)
        return Data(frame)
    }

    func appendPlayback(_ data: Data) {
        lock.lock()
        data.withUnsafeBytes { bytes in
            let samples = bytes.bindMemory(to: Int16.self)
            playback.append(contentsOf: samples.map { Float(Int16(littleEndian: $0)) / Float(Int16.max) })
        }
        lock.unlock()
    }

    func render(_ output: UnsafeMutableAudioBufferListPointer) {
        lock.lock()
        defer { lock.unlock() }
        for buffer in output {
            guard let memory = buffer.mData else { continue }
            let samples = memory.assumingMemoryBound(to: Float.self)
            let count = Int(buffer.mDataByteSize) / MemoryLayout<Float>.size
            for index in 0..<count {
                samples[index] = playback.isEmpty ? 0 : playback.removeFirst()
            }
        }
    }
}

private func deviceName(_ device: AudioObjectID) -> String? {
    var address = AudioObjectPropertyAddress(
        mSelector: kAudioObjectPropertyName,
        mScope: kAudioObjectPropertyScopeGlobal,
        mElement: kAudioObjectPropertyElementMain
    )
    var value: Unmanaged<CFString>?
    var size = UInt32(MemoryLayout<Unmanaged<CFString>?>.size)
    let status = withUnsafeMutablePointer(to: &value) { pointer in
        AudioObjectGetPropertyData(device, &address, 0, nil, &size, UnsafeMutableRawPointer(pointer))
    }
    guard status == noErr else { return nil }
    return value?.takeUnretainedValue() as String?
}

private func audioDevices() throws -> [AudioObjectID] {
    var address = AudioObjectPropertyAddress(
        mSelector: kAudioHardwarePropertyDevices,
        mScope: kAudioObjectPropertyScopeGlobal,
        mElement: kAudioObjectPropertyElementMain
    )
    var size: UInt32 = 0
    let sizeStatus = AudioObjectGetPropertyDataSize(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size)
    guard sizeStatus == noErr else { throw NSError(domain: "CoreAudio", code: Int(sizeStatus)) }
    var devices = [AudioObjectID](repeating: 0, count: Int(size) / MemoryLayout<AudioObjectID>.size)
    let listStatus = devices.withUnsafeMutableBufferPointer { buffer in
        AudioObjectGetPropertyData(AudioObjectID(kAudioObjectSystemObject), &address, 0, nil, &size, buffer.baseAddress!)
    }
    guard listStatus == noErr else { throw NSError(domain: "CoreAudio", code: Int(listStatus)) }
    return devices
}

private func findDevice(named name: String) throws -> AudioObjectID {
    for device in try audioDevices() where deviceName(device) == name {
        return device
    }
    throw NSError(domain: "CoreAudio", code: -1, userInfo: [NSLocalizedDescriptionKey: "Audio device not found: \(name)"])
}

private func readExactly(_ count: Int) throws -> Data {
    var data = Data()
    while data.count < count {
        guard let next = try FileHandle.standardInput.read(upToCount: count - data.count), !next.isEmpty else {
            throw NSError(domain: "sim-deck", code: -1, userInfo: [NSLocalizedDescriptionKey: "PCM input closed"])
        }
        data.append(next)
    }
    return data
}

do {
    let inputDevice = try findDevice(named: "AC Interface")
    let outputDevice = try findDevice(named: "AS Interface")
    let pcm = PCMBuffer()
    let queue = DispatchQueue(label: "sim-deck.coreaudio")
    var inputProc: AudioDeviceIOProcID?
    var outputProc: AudioDeviceIOProcID?

    let inputStatus = AudioDeviceCreateIOProcIDWithBlock(&inputProc, inputDevice, queue) { _, input, _, _, _ in
        let buffers = UnsafeMutableAudioBufferListPointer(UnsafeMutablePointer(mutating: input))
        for buffer in buffers {
            guard let memory = buffer.mData else { continue }
            let samples = memory.assumingMemoryBound(to: Float.self)
            pcm.appendCapture(UnsafeBufferPointer(start: samples, count: Int(buffer.mDataByteSize) / MemoryLayout<Float>.size))
        }
    }
    guard inputStatus == noErr, let inputProc else { throw NSError(domain: "CoreAudio", code: Int(inputStatus)) }

    let outputStatus = AudioDeviceCreateIOProcIDWithBlock(&outputProc, outputDevice, queue) { _, _, _, output, _ in
        pcm.render(UnsafeMutableAudioBufferListPointer(output))
    }
    guard outputStatus == noErr, let outputProc else {
        AudioDeviceDestroyIOProcID(inputDevice, inputProc)
        throw NSError(domain: "CoreAudio", code: Int(outputStatus))
    }

    let inputStart = AudioDeviceStart(inputDevice, inputProc)
    guard inputStart == noErr else { throw NSError(domain: "CoreAudio", code: Int(inputStart)) }
    let outputStart = AudioDeviceStart(outputDevice, outputProc)
    guard outputStart == noErr else { throw NSError(domain: "CoreAudio", code: Int(outputStart)) }
    FileHandle.standardError.write(Data("COREAUDIO_READY input=AC Interface output=AS Interface format=float32/8000Hz/mono\n".utf8))

    DispatchQueue.global(qos: .userInitiated).async {
        while true {
            if let frame = pcm.nextCaptureFrame() {
                do { try FileHandle.standardOutput.write(contentsOf: frame) }
                catch { exit(1) }
            } else {
                Thread.sleep(forTimeInterval: 0.002)
            }
        }
    }

    while true { pcm.appendPlayback(try readExactly(frameBytes)) }
} catch {
    FileHandle.standardError.write(Data("COREAUDIO_ERROR \(error.localizedDescription)\n".utf8))
    exit(1)
}
