void mainImage(out vec4 c, in vec2 p) {
ivec2 cell=ivec2(p/16.0); float v=float(cell.x%2); bvec2 b=bvec2(true,false); c=vec4(vec3(v),1.0);
}
