void mainImage(out vec4 c, in vec2 p) {
vec2 uv=(p-0.5*iResolution.xy)/iResolution.y; float d=length(uv); c=vec4(vec3(smoothstep(0.3,0.29,d)),1.0);
}
