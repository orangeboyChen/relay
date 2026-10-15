# iOS Architecture

The source tree is organized by ownership rather than by file type.

- `SimDeck/App`: application entry point, root navigation, and app-wide observable state.
- `SimDeck/Core`: shared domain models and platform infrastructure with no feature UI.
- `SimDeck/DesignSystem`: reusable visual components shared by multiple features.
- `SimDeck/Features`: vertical feature slices for calls, messages, profile, devices, and settings.
- `SimDeck/PreviewSupport`: deterministic fixtures and service doubles compiled only in Debug builds.
- `SimDeck/Resources`: localization catalogs and other application resources.
- `SimDeckTests`: tests arranged to mirror the corresponding production ownership boundary.

Screens should remain thin composition roots. Reusable sections belong in dedicated `View` types with narrow inputs, and feature-specific models and services should stay inside their feature unless more than one feature owns them.
