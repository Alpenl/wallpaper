void mainImage(out vec4 c, in vec2 p) {
vec2 uv=p/iResolution.xy; if(uv.x>0.5){c=vec4(1.0);}else{c=vec4(0.0,0.0,0.0,1.0);}
}
