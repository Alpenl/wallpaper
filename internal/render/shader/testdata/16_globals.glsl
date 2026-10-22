precision mediump float;
const float scale=0.01;
void mainImage(out vec4 c, in vec2 p) {
float v=sin(p.x*scale+iTime); c=vec4(vec3(v*.5+.5),1.0);
}
