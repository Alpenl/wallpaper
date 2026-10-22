void mainImage(out vec4 c, vec2 p) {
 mat2 base=mat2(vec2(1.0,2.0),3.0,4.0);
 mat4 large=mat4(base);
 mat3 reduced=mat3(large);
 vec3 col=reduced[1];
 c=vec4(col,1.0);
}
