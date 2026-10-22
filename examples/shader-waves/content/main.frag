// Native single-pass wallpaper. Mouse and audio are reserved zero inputs.
#define TAU 6.2831853
void mainImage(out vec4 color, in vec2 pixel) {
    vec2 uv = pixel / iResolution.xy;
    float wave = sin(TAU * (uv.x * 2.0 + uv.y) + iTime * speed);
    float glow = 0.5 + 0.5 * wave;
    vec3 rgb = mix(vec3(0.02, 0.03, 0.07), tint, glow);
    color = vec4(enabled ? rgb : vec3(0.0), 1.0);
}
