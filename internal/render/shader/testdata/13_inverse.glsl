void mainImage(out vec4 c, in vec2 p) {
vec2 q=(p-0.5*iResolution.xy)/iResolution.y; float d=inversesqrt(dot(q,q)+1e-2); c=vec4(vec3(d*0.1),1.0);
}
