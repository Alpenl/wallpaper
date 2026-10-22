vec3 palette(in float t) { return vec3(t,0.5,1.0-t); }
void adjust(inout vec3 v) { v *= 0.8; }
void mainImage(out vec4 c, in vec2 p) {
vec3 col=palette(p.x/iResolution.x); adjust(col); c=vec4(col,1.0);
}
