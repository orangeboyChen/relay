// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

import PhotosUI
import SwiftUI
import UIKit
import UniformTypeIdentifiers

struct MessageComposer: View {
    @Binding var draft: String
    @Binding private var attachment: MessageImageAttachment?
    @Binding private var selectedLineID: UUID?

    let availableLines: [DeviceLineIdentity]
    let showsLinePicker: Bool
    let isTextEntryEnabled: Bool
    let canSendOverride: Bool?
    let sendAction: () -> Void

    @FocusState private var isTextFieldFocused: Bool
    @State private var photoSelection: PhotosPickerItem?
    @State private var isPhotoPickerPresented = false
    @State private var isCameraPresented = false
    @State private var isFileImporterPresented = false

    init(
        draft: Binding<String>,
        attachment: Binding<MessageImageAttachment?> = .constant(nil),
        availableLines: [DeviceLineIdentity] = [],
        selectedLineID: Binding<UUID?> = .constant(nil),
        showsLinePicker: Bool = false,
        isTextEntryEnabled: Bool = true,
        canSend: Bool? = nil,
        sendAction: @escaping () -> Void = {}
    ) {
        _draft = draft
        _attachment = attachment
        _selectedLineID = selectedLineID
        self.availableLines = availableLines
        self.showsLinePicker = showsLinePicker
        self.isTextEntryEnabled = isTextEntryEnabled
        canSendOverride = canSend
        self.sendAction = sendAction
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            if let attachment {
                attachmentPreview(attachment)
                    .transition(.scale(scale: 0.96, anchor: .bottomLeading).combined(with: .opacity))
            }

            HStack(alignment: .bottom, spacing: 8) {
                attachmentMenu
                inputCapsule
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 5)
        .animation(.snappy, value: attachment?.id)
        .photosPicker(
            isPresented: $isPhotoPickerPresented,
            selection: $photoSelection,
            matching: .images,
            preferredItemEncoding: .compatible
        )
        .onChange(of: photoSelection) { _, item in
            guard let item else { return }
            Task {
                defer { photoSelection = nil }
                guard let data = try? await item.loadTransferable(type: Data.self) else { return }
                await setAttachment(from: data, suggestedFilename: "photo.jpg")
            }
        }
        .sheet(isPresented: $isCameraPresented) {
            MessageCameraPicker { image in
                guard let data = image.jpegData(compressionQuality: 0.9) else { return }
                Task {
                    await setAttachment(from: data, suggestedFilename: "camera.jpg")
                }
            }
            .ignoresSafeArea()
        }
        .fileImporter(
            isPresented: $isFileImporterPresented,
            allowedContentTypes: [.image],
            allowsMultipleSelection: false
        ) { result in
            guard case let .success(urls) = result, let url = urls.first else { return }
            let didAccess = url.startAccessingSecurityScopedResource()
            defer {
                if didAccess {
                    url.stopAccessingSecurityScopedResource()
                }
            }
            guard let data = try? Data(contentsOf: url) else { return }
            Task {
                await setAttachment(from: data, suggestedFilename: url.lastPathComponent)
            }
        }
    }

    private var attachmentMenu: some View {
        Menu {
            Button {
                isPhotoPickerPresented = true
            } label: {
                Label("Photos", systemImage: "photo.on.rectangle")
            }

            Button {
                isCameraPresented = true
            } label: {
                Label("Camera", systemImage: "camera")
            }

            Button {
                isFileImporterPresented = true
            } label: {
                Label("Choose File", systemImage: "folder")
            }
        } label: {
            Image(systemName: "plus")
                .font(.system(size: 19, weight: .medium))
                .frame(width: 44, height: 44)
                .contentShape(.circle)
        }
        .buttonStyle(.plain)
        .glassEffect(.regular.interactive(), in: Circle())
        .disabled(!isTextEntryEnabled)
        .accessibilityLabel("Add attachment")
    }

    private var inputCapsule: some View {
        HStack(alignment: .bottom, spacing: 4) {
            if showsLinePicker {
                linePicker

                Divider()
                    .frame(height: 24)
                    .padding(.vertical, 10)
            }

            TextField("Text Message · MMS", text: $draft, axis: .vertical)
                .lineLimit(1...5)
                .focused($isTextFieldFocused)
                .padding(.leading, showsLinePicker ? 4 : 12)
                .padding(.vertical, 7)
                .frame(minHeight: 44)
                .disabled(!isTextEntryEnabled)

            if canSend {
                Button(action: sendAction) {
                    Image(systemName: "arrow.up.circle.fill")
                        .font(.system(size: 30))
                        .frame(width: 44, height: 44)
                }
                .buttonStyle(PressFeedbackButtonStyle(pressedScale: 0.9))
                .accessibilityLabel("Send message")
                .transition(.scale.combined(with: .opacity))
            } else {
                Image(systemName: "waveform")
                    .font(.system(size: 19, weight: .medium))
                    .foregroundStyle(.tertiary)
                    .frame(width: 44, height: 44)
                    .accessibilityLabel("Audio messages are unavailable")
                    .transition(.scale.combined(with: .opacity))
            }
        }
        .frame(minHeight: 44)
        .glassEffect(.regular, in: Capsule())
    }

    private func attachmentPreview(_ attachment: MessageImageAttachment) -> some View {
        ZStack(alignment: .topTrailing) {
            MessageImage(attachment: attachment)
                .frame(width: 96, height: 72)
                .clipShape(RoundedRectangle(cornerRadius: 14, style: .continuous))

            Button {
                self.attachment = nil
            } label: {
                Image(systemName: "xmark.circle.fill")
                    .symbolRenderingMode(.palette)
                    .foregroundStyle(.white, .black.opacity(0.62))
                    .font(.title3)
                    .frame(width: 32, height: 32)
            }
            .buttonStyle(.plain)
            .offset(x: 9, y: -9)
            .accessibilityLabel("Remove attachment")
        }
        .padding(.leading, 52)
    }

    private var canSend: Bool {
        canSendOverride
            ?? (attachment != nil
                || !draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
    }

    private var selectedLine: DeviceLineIdentity? {
        availableLines.first(where: { $0.id == selectedLineID }) ?? availableLines.first
    }

    private var linePicker: some View {
        Menu {
            ForEach(availableLines) { line in
                Button {
                    selectedLineID = line.id
                } label: {
                    HStack {
                        SIMLabel(line: line)
                        if line.id == selectedLine?.id {
                            Image(systemName: "checkmark")
                        }
                    }
                }
            }
        } label: {
            Group {
                if let selectedLine {
                    SIMBadge(line: selectedLine, size: 18)
                } else {
                    Image(systemName: "simcard")
                        .foregroundStyle(.secondary)
                }
            }
            .frame(width: 36, height: 44)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Choose sending SIM")
    }

    @MainActor
    private func setAttachment(from data: Data, suggestedFilename: String) async {
        guard let processed = MessageImageProcessor.makeAttachment(
            from: data,
            suggestedFilename: suggestedFilename
        ) else { return }
        attachment = processed
    }
}

private enum MessageImageProcessor {
    static func makeAttachment(
        from data: Data,
        suggestedFilename: String
    ) -> MessageImageAttachment? {
        guard let image = UIImage(data: data) else { return nil }
        let resized = resizedImage(image, maximumDimension: 960)
        let compressionQualities: [CGFloat] = [0.8, 0.65, 0.5, 0.4, 0.3, 0.22]
        let encoded = compressionQualities.lazy
            .compactMap { resized.jpegData(compressionQuality: $0) }
            .first(where: { $0.count <= 36_000 })
            ?? resizedImage(image, maximumDimension: 640).jpegData(compressionQuality: 0.2)
        guard let encoded else { return nil }
        let base = URL(fileURLWithPath: suggestedFilename)
            .deletingPathExtension()
            .lastPathComponent
        let safeBase = base.isEmpty ? "photo" : base
        return MessageImageAttachment(
            data: encoded,
            mimeType: "image/jpeg",
            filename: "\(safeBase).jpg"
        )
    }

    private static func resizedImage(_ image: UIImage, maximumDimension: CGFloat) -> UIImage {
        let largestDimension = max(image.size.width, image.size.height)
        guard largestDimension > maximumDimension else { return image }
        let scale = maximumDimension / largestDimension
        let targetSize = CGSize(
            width: floor(image.size.width * scale),
            height: floor(image.size.height * scale)
        )
        return UIGraphicsImageRenderer(size: targetSize).image { _ in
            image.draw(in: CGRect(origin: .zero, size: targetSize))
        }
    }
}

private struct MessageCameraPicker: UIViewControllerRepresentable {
    let completion: (UIImage) -> Void

    @Environment(\.dismiss) private var dismiss

    func makeCoordinator() -> Coordinator {
        Coordinator(completion: completion, dismiss: dismiss)
    }

    func makeUIViewController(context: Context) -> UIImagePickerController {
        let controller = UIImagePickerController()
        controller.sourceType = .camera
        controller.mediaTypes = [UTType.image.identifier]
        controller.delegate = context.coordinator
        return controller
    }

    func updateUIViewController(_ uiViewController: UIImagePickerController, context: Context) {}

    final class Coordinator: NSObject, UIImagePickerControllerDelegate, UINavigationControllerDelegate {
        let completion: (UIImage) -> Void
        let dismiss: DismissAction

        init(completion: @escaping (UIImage) -> Void, dismiss: DismissAction) {
            self.completion = completion
            self.dismiss = dismiss
        }

        func imagePickerController(
            _ picker: UIImagePickerController,
            didFinishPickingMediaWithInfo info: [UIImagePickerController.InfoKey: Any]
        ) {
            if let image = info[.originalImage] as? UIImage {
                completion(image)
            }
            dismiss()
        }

        func imagePickerControllerDidCancel(_ picker: UIImagePickerController) {
            dismiss()
        }
    }
}

#if DEBUG
#Preview("Message composer") {
    @Previewable @State var draft = ""
    @Previewable @State var attachment: MessageImageAttachment?

    VStack {
        Spacer()
        MessageComposer(draft: $draft, attachment: $attachment)
    }
}

#Preview("Message composer - Accessibility") {
    @Previewable @State var draft = "A longer message that wraps onto more than one line."
    @Previewable @State var attachment: MessageImageAttachment?

    VStack {
        Spacer()
        MessageComposer(draft: $draft, attachment: $attachment)
    }
    .preferredColorScheme(.dark)
    .environment(\.dynamicTypeSize, .accessibility3)
}
#endif
