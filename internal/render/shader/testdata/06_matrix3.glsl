void mainImage(out vec4 c, in vec2 p) {
mat3 m=mat3(1.0); vec3 q=m*vec3(p/iResolution.xy,0.5); c=vec4(q,1.0);
}
