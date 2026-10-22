void mainImage(out vec4 c, in vec2 p) {
vec2 uv = p / iResolution.xy; vec3 col = 0.5 + 0.5*cos(iTime + uv.xyx + vec3(0.0,2.0,4.0)); c=vec4(col,1.0);
}
