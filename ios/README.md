# relay-ios

`relay-ios` is the iOS 18 SwiftUI client for encrypted SIM messaging and cellular calling through sim-deck devices.

Every screen and reusable component must provide deterministic `#Preview` coverage without contacting live services.

## TestFlight

Copy `fastlane/.env.example` to `fastlane/.env`, fill in the App Store Connect API key values, and keep the `.p8` file outside the repository. App Store Connect API key authentication is Fastlane's recommended method and avoids interactive 2FA. A valid Apple Distribution certificate and provisioning profile must also be available to Xcode for the archive export.

Then run:

```sh
bundle install
bundle exec fastlane ios beta
```

The lane reads the current marketing version, compares the local build number with the latest TestFlight build for that version, writes the next build number to the Xcode project with Fastlane's `increment_build_number`, archives the app, and uploads it to TestFlight.

To upload a different marketing version or attach TestFlight release notes:

```sh
bundle exec fastlane ios beta version:0.2.0 changelog:"Describe this beta"
```

Passing `version:` first updates the marketing version with Fastlane's `increment_version_number`; without it, the lane keeps the current marketing version and only advances the build number.

References: [Fastlane beta deployment](https://docs.fastlane.tools/getting-started/ios/beta-deployment/), [App Store Connect API key](https://docs.fastlane.tools/app-store-connect-api/), and [increment_build_number](https://docs.fastlane.tools/actions/increment_build_number/).

## License

Copyright (C) 2026 orangeboyChen.

Original Sim Deck iOS source code and resources in this repository are licensed
under the Mozilla Public License 2.0 (`MPL-2.0`). See [LICENSE](LICENSE). The
bundled WebRTC framework retains its own license, reproduced in
[`SimDeck/Resources/WebRTC-License.txt`](SimDeck/Resources/WebRTC-License.txt).

When distributing a compiled app, provide recipients with access to the
corresponding MPL-covered source code for that build. The app's About screen
links to this repository; release builds should identify their source revision.
