void mainImage(out vec4 c, const in highp vec2 p) {
 mat2 rotation=mat2(1.0);
 { vec2 rotation=p.st; c=vec4(rotation,0.0,1.0); }
 vec2 q=rotation*p.yx;
 vec4 full=vec4(q,0.5,1.0);
 c=vec4(vec3(full),1.0);
}
