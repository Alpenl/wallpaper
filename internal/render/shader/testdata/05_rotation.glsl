void mainImage(out vec4 c, in vec2 p) {
float a=iTime; mat2 r=mat2(cos(a),sin(a),-sin(a),cos(a)); vec2 q=r*(p/iResolution.xy-0.5); c=vec4(q+0.5,0.0,1.0);
}
