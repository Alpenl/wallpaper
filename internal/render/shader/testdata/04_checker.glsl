void mainImage(out vec4 c, in vec2 p) {
vec2 q=floor(p/32.0); float v=mod(q.x+q.y,2.0); c=vec4(vec3(v),1.0);
}
