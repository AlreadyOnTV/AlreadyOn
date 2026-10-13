# Display settings

AlreadyOn has no in-app brightness or font-size controls in WebUI, Android phone or Google TV. Use your device or display brightness controls, browser zoom, and operating-system text/accessibility settings.

WebUI uses its normal typography without a global dimming overlay or per-profile text scale. Previously saved brightness and font-level preferences no longer affect it. Native Android typography continues using scalable pixels and respects system font scaling.

The legacy nullable profile API fields `fontLevel` and `brightnessLevel` remain readable/writable for older clients and retained installations. Current clients do not apply or edit them. This change does not clear profile data or modify device brightness settings. See [OpenAPI](../api/openapi.yaml).
