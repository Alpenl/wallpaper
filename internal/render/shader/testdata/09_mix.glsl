void mainImage(out vec4 c, in vec2 p) {
vec3 a=vec3(1.0,0.0,0.2); vec3 b=vec3(0.0,0.2,1.0); c=vec4(mix(a,b,p.x/iResolution.x),1.0);
}
