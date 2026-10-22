void mainImage(out vec4 c, in vec2 p) {
float v=0.0; for(int i=0;i<5;i++){v+=sin(float(i)+p.x*0.01+iTime);} c=vec4(vec3(v*0.1+0.5),1.0);
}
