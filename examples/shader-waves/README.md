# Native waves

Import this folder (or zip its contents into a `.wpk`). It uses the native
D3D11/Metal shader host, with no HTML or WebView. `preview.png` is a generated
placeholder; import attempts to replace it with an isolated GPU-rendered frame.

`speed`, `tint`, and `enabled` are uniforms declared by the manifest. The native
side panel stores their values along with FPS and render-scale overrides.
The initial render scale is 0.75 and the initial FPS cap is 30. Battery and saver
policy may reduce the cap. Linux can import the package but cannot render it.
