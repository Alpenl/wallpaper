void mainImage(out vec4 c, in vec2 p) {
mat4 m=mat4(2.0); c=m*vec4(p/iResolution.xy,0.0,1.0);
}
