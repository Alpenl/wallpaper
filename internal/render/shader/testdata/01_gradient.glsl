void mainImage(out vec4 c, in vec2 p) {
vec2 uv = p / iResolution.xy; c = vec4(uv, 0.5, 1.0);
}
