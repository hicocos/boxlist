FROM python:3.13-slim
ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    OPENLIST_ICONS_ROOT=/var/lib/openlist-icons \
    OPENLIST_ICONS_BACKEND=http://127.0.0.1:5244 \
    OPENLIST_ICONS_ORIGIN=https://openlist.example
WORKDIR /app
COPY requirements.txt ./requirements.txt
RUN python -m pip install --no-cache-dir -r requirements.txt \
    && python -c "from PIL import features; assert features.check('webp')"
COPY icon_service ./icon_service
USER 999:989
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["python", "-c", "import json,urllib.request; r=urllib.request.urlopen('http://127.0.0.1:5235/icon-library/api/health',timeout=3); assert r.status==200 and json.load(r)=={'code':200,'data':{'version':1}}"]
CMD ["python", "-m", "icon_service"]
